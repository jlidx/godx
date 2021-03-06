// Copyright 2019 DxChain, All rights reserved.
// Use of this source code is governed by an Apache
// License 2.0 that can be found in the LICENSE file.

package storagehost

import (
	"errors"
	"fmt"
	"sort"

	"github.com/DxChainNetwork/godx/accounts"
	"github.com/DxChainNetwork/godx/common"
	"github.com/DxChainNetwork/godx/core/types"
	"github.com/DxChainNetwork/godx/crypto/merkle"
	"github.com/DxChainNetwork/godx/log"
	"github.com/DxChainNetwork/godx/p2p"
	"github.com/DxChainNetwork/godx/storage"
)

// UploadHandler handles the upload negotiation
func UploadHandler(h *StorageHost, sp storage.Peer, uploadReqMsg p2p.Msg) {
	var hostNegotiateErr, clientNegotiateErr, clientCommitErr error

	defer func() {
		if clientNegotiateErr != nil || clientCommitErr != nil {
			_ = sp.SendHostAckMsg()
			h.ethBackend.CheckAndUpdateConnection(sp.PeerNode())
		} else if hostNegotiateErr != nil {
			_ = sp.SendHostNegotiateErrorMsg()
		}
	}()

	// Read upload request
	var uploadRequest storage.UploadRequest
	if err := uploadReqMsg.Decode(&uploadRequest); err != nil {
		clientNegotiateErr = fmt.Errorf("failed to decode the upload request message: %s", err.Error())
		return
	}

	// Get revision from storage responsibility
	h.lock.RLock()
	so, err := getStorageResponsibility(h.db, uploadRequest.StorageContractID)
	snapshotSo := so
	h.lock.RUnlock()

	// it is normal not getting storage responsibility
	if err != nil {
		hostNegotiateErr = fmt.Errorf("failed to get storage responsibility: %s", err.Error())
		return
	}

	settings := h.externalConfig()
	currentBlockHeight := h.blockHeight
	currentRevision := so.StorageContractRevisions[len(so.StorageContractRevisions)-1]

	// Process each action
	newRoots := append([]common.Hash(nil), so.SectorRoots...)
	sectorsChanged := make(map[uint64]struct{})

	var bandwidthRevenue common.BigInt
	var sectorsGained []common.Hash
	var gainedSectorData [][]byte
	for _, action := range uploadRequest.Actions {
		switch action.Type {
		case storage.UploadActionAppend:
			// Update sector roots.
			newRoot := merkle.Sha256MerkleTreeRoot(action.Data)
			newRoots = append(newRoots, newRoot)
			sectorsGained = append(sectorsGained, newRoot)
			gainedSectorData = append(gainedSectorData, action.Data)

			sectorsChanged[uint64(len(newRoots))-1] = struct{}{}

			// Update finances
			bandwidthRevenue = bandwidthRevenue.Add(settings.UploadBandwidthPrice.MultUint64(storage.SectorSize))
		default:
			hostNegotiateErr = fmt.Errorf("unknown upload action type: %s", action.Type)
		}
	}

	//var storageRevenue, newDeposit *big.Int
	var storageRevenue, newDeposit common.BigInt

	if len(newRoots) > len(so.SectorRoots) {
		bytesAdded := storage.SectorSize * uint64(len(newRoots)-len(so.SectorRoots))
		blocksRemaining := so.proofDeadline() - currentBlockHeight
		blockBytesCurrency := common.NewBigIntUint64(blocksRemaining).Mult(common.NewBigIntUint64(bytesAdded))
		storageRevenue = blockBytesCurrency.Mult(settings.StoragePrice)
		newDeposit = newDeposit.Add(blockBytesCurrency.Mult(settings.Deposit))
	}

	// If a Merkle proof was requested, construct it
	newMerkleRoot := merkle.Sha256CachedTreeRoot2(newRoots)

	// Construct the new revision
	newRevision := currentRevision
	newRevision.NewRevisionNumber = uploadRequest.NewRevisionNumber
	for _, action := range uploadRequest.Actions {
		if action.Type == storage.UploadActionAppend {
			newRevision.NewFileSize += storage.SectorSize
		}
	}
	newRevision.NewFileMerkleRoot = newMerkleRoot
	newRevision.NewValidProofOutputs = make([]types.DxcoinCharge, len(currentRevision.NewValidProofOutputs))
	for i := range newRevision.NewValidProofOutputs {
		newRevision.NewValidProofOutputs[i] = types.DxcoinCharge{
			Value:   uploadRequest.NewValidProofValues[i],
			Address: currentRevision.NewValidProofOutputs[i].Address,
		}
	}
	newRevision.NewMissedProofOutputs = make([]types.DxcoinCharge, len(currentRevision.NewMissedProofOutputs))
	for i := range newRevision.NewMissedProofOutputs {
		newRevision.NewMissedProofOutputs[i] = types.DxcoinCharge{
			Value:   uploadRequest.NewMissedProofValues[i],
			Address: currentRevision.NewMissedProofOutputs[i].Address,
		}
	}

	// Verify the new revision
	newRevenue := storageRevenue.Add(bandwidthRevenue).Add(settings.BaseRPCPrice)

	so.SectorRoots, newRoots = newRoots, so.SectorRoots
	if err := VerifyRevision(&so, &newRevision, currentBlockHeight, newRevenue, newDeposit); err != nil {
		hostNegotiateErr = fmt.Errorf("revision verification failed. contractID: %s, err: %s", newRevision.ParentID.String(), err.Error())
		return
	}
	so.SectorRoots, newRoots = newRoots, so.SectorRoots

	var merkleResp storage.UploadMerkleProof
	// Calculate which sectors changed
	oldNumSectors := uint64(len(so.SectorRoots))
	proofRanges := make([]merkle.SubTreeLimit, 0, len(sectorsChanged))
	for index := range sectorsChanged {
		if index < oldNumSectors {
			proofRanges = append(proofRanges, merkle.SubTreeLimit{
				Left:  index,
				Right: index + 1,
			})
		}
	}
	sort.Slice(proofRanges, func(i, j int) bool {
		return proofRanges[i].Left < proofRanges[j].Left
	})
	// Record old leaf hashes for all changed sectors
	leafHashes := make([]common.Hash, len(proofRanges))
	for i, r := range proofRanges {
		leafHashes[i] = so.SectorRoots[r.Left]
	}

	// Construct the merkle proof
	oldHashSet, err := merkle.Sha256DiffProof(so.SectorRoots, proofRanges, oldNumSectors)
	if err != nil {
		hostNegotiateErr = fmt.Errorf("error construct the merkle proof: %s", err.Error())
		return
	}

	merkleResp = storage.UploadMerkleProof{
		OldSubtreeHashes: oldHashSet,
		OldLeafHashes:    leafHashes,
		NewMerkleRoot:    newMerkleRoot,
	}

	// Calculate bandwidth cost of proof
	proofSize := storage.HashSize * (len(merkleResp.OldSubtreeHashes) + len(leafHashes) + 1)
	bandwidthRevenue = bandwidthRevenue.Add(settings.DownloadBandwidthPrice.Mult(common.NewBigInt(int64(proofSize))))

	if err := sp.SendUploadMerkleProof(merkleResp); err != nil {
		log.Error("storage host failed to send merkle proof to the storage client", "err", err)
		return
	}

	var clientRevisionSign []byte
	msg, err := sp.HostWaitContractResp()
	if err != nil {
		log.Error("after the merkle proof was sent, failed to get the storage client's response", "err", err)
		return
	}

	if msg.Code == storage.ClientNegotiateErrorMsg {
		clientNegotiateErr = storage.ErrClientNegotiate
		return
	}

	if err = msg.Decode(&clientRevisionSign); err != nil {
		clientNegotiateErr = fmt.Errorf("failed to decode the client revision sign: %s", err.Error())
		return
	}

	// Sign host's revision and send it to client
	account := accounts.Account{Address: newRevision.NewValidProofOutputs[1].Address}
	wallet, err := h.am.Find(account)
	if err != nil {
		hostNegotiateErr = fmt.Errorf("host failed to get the account address: %s", err.Error())
		return
	}

	hostSig, err := wallet.SignHash(account, newRevision.RLPHash().Bytes())
	if err != nil {
		hostNegotiateErr = fmt.Errorf("host failed to sign the new contract revision")
		return
	}

	newRevision.Signatures = [][]byte{clientRevisionSign, hostSig}

	// Update the storage responsibility
	so.SectorRoots = newRoots
	so.PotentialStorageRevenue = so.PotentialStorageRevenue.Add(storageRevenue)
	so.RiskedStorageDeposit = so.RiskedStorageDeposit.Add(newDeposit)
	so.PotentialUploadRevenue = so.PotentialUploadRevenue.Add(bandwidthRevenue)
	so.StorageContractRevisions = append(so.StorageContractRevisions, newRevision)

	// send the host revision sign
	if err := sp.SendUploadHostRevisionSign(hostSig); err != nil {
		log.Error("failed to send the upload host revision sign", "err", err)
		return
	}

	// wait for client commit success msg
	msg, err = sp.HostWaitContractResp()
	if err != nil {
		log.Error("storage host failed to get client commit success msg", "err", err)
		return
	}

	if msg.Code == storage.ClientCommitSuccessMsg {
		err = h.modifyStorageResponsibility(so, nil, sectorsGained, gainedSectorData)
		if err != nil {
			_ = sp.SendHostCommitFailedMsg()

			// wait for client ack msg
			msg, err = sp.HostWaitContractResp()
			if err != nil {
				log.Error("storage host failed to get client ack msg", "err", err)
				return
			}

			// host send the last ack msg and return
			_ = sp.SendHostAckMsg()
			return
		}
	} else if msg.Code == storage.ClientCommitFailedMsg {
		clientCommitErr = storage.ErrClientCommit
		return
	} else if msg.Code == storage.ClientNegotiateErrorMsg {
		clientNegotiateErr = storage.ErrClientNegotiate
		return
	}

	// In case the user restart the program, the contract create handler will not be
	// reached. Therefore, needs another check in the upload handler
	if !sp.IsStaticConn() {
		node := sp.PeerNode()
		if node == nil {
			return
		}
		h.ethBackend.SetStatic(node)
	}

	// send host 'ACK' msg to client
	if err := sp.SendHostAckMsg(); err != nil {
		log.Error("storage host failed to send host ack msg", "err", err)
		_ = h.rollbackStorageResponsibility(snapshotSo, sectorsGained, nil, nil)
		h.ethBackend.CheckAndUpdateConnection(sp.PeerNode())
	}
}

// VerifyRevision checks that the revision pays the host correctly, and that
// the revision does not attempt any malicious or unexpected changes.
func VerifyRevision(so *StorageResponsibility, revision *types.StorageContractRevision, blockHeight uint64, expectedExchange, expectedCollateral common.BigInt) error {
	// Check that the revision is well-formed.
	if len(revision.NewValidProofOutputs) != 2 || len(revision.NewMissedProofOutputs) != 2 {
		return errBadContractOutputCounts
	}

	// Check that the time to finalize and submit the file contract revision
	// has not already passed.
	if so.expiration()-postponedExecutionBuffer <= blockHeight {
		return errLateRevision
	}

	oldFCR := so.StorageContractRevisions[len(so.StorageContractRevisions)-1]

	// Host payout addresses shouldn't change
	if revision.NewValidProofOutputs[1].Address != oldFCR.NewValidProofOutputs[1].Address {
		return errors.New("host payout address changed")
	}
	if revision.NewMissedProofOutputs[1].Address != oldFCR.NewMissedProofOutputs[1].Address {
		return errors.New("host payout address changed")
	}

	// Check that all non-volatile fields are the same.
	if oldFCR.ParentID != revision.ParentID {
		return errBadContractParent
	}
	if oldFCR.UnlockConditions.UnlockHash() != revision.UnlockConditions.UnlockHash() {
		return errBadUnlockConditions
	}
	if oldFCR.NewRevisionNumber >= revision.NewRevisionNumber {
		return errBadRevisionNumber
	}
	if revision.NewFileSize != uint64(len(so.SectorRoots))*storage.SectorSize {
		return errBadFileSize
	}
	if oldFCR.NewWindowStart != revision.NewWindowStart {
		return errBadWindowStart
	}
	if oldFCR.NewWindowEnd != revision.NewWindowEnd {
		return errBadWindowEnd
	}
	if oldFCR.NewUnlockHash != revision.NewUnlockHash {
		return errBadUnlockHash
	}

	// Determine the amount that was transferred from the client.
	if revision.NewValidProofOutputs[0].Value.Cmp(oldFCR.NewValidProofOutputs[0].Value) > 0 {
		return fmt.Errorf("client increased its valid proof output: %v", errHighClientValidOutput)
	}
	fromClient := common.NewBigInt(oldFCR.NewValidProofOutputs[0].Value.Int64()).Sub(common.NewBigInt(revision.NewValidProofOutputs[0].Value.Int64()))
	// Verify that enough money was transferred.
	if fromClient.Cmp(expectedExchange) < 0 {
		s := fmt.Sprintf("expected at least %v to be exchanged, but %v was exchanged: ", expectedExchange, fromClient)
		return ExtendErr(s, errHighClientValidOutput)
	}

	// Determine the amount of money that was transferred to the host.
	if oldFCR.NewValidProofOutputs[1].Value.Cmp(revision.NewValidProofOutputs[1].Value) > 0 {
		return ExtendErr("host valid proof output was decreased: ", errLowHostValidOutput)
	}
	toHost := common.NewBigInt(revision.NewValidProofOutputs[1].Value.Int64()).Sub(common.NewBigInt(oldFCR.NewValidProofOutputs[1].Value.Int64()))

	// Verify that enough money was transferred.
	if toHost.Cmp(fromClient) != 0 {
		s := fmt.Sprintf("expected exactly %v to be transferred to the host, but %v was transferred: ", fromClient, toHost)
		return ExtendErr(s, errLowHostValidOutput)
	}

	// If the client's valid proof output is larger than the client's missed
	// proof output, the client has incentive to see the host fail. Make sure
	// that this incentive is not present.
	if revision.NewValidProofOutputs[0].Value.Cmp(revision.NewMissedProofOutputs[0].Value) > 0 {
		return ExtendErr("client has incentive to see host fail: ", errHighClientMissedOutput)
	}

	// Check that the host is not going to be posting more collateral than is
	// expected. If the new misesd output is greater than the old one, the host
	// is actually posting negative collateral, which is fine.
	//if revision.NewMissedProofOutputs[1].Value.Cmp(oldFCR.NewMissedProofOutputs[1].Value) <= 0 {
	//	collateral := common.NewBigInt(oldFCR.NewMissedProofOutputs[1].Value.Int64()).Sub(common.NewBigInt(revision.NewMissedProofOutputs[1].Value.Int64()))
	//	if collateral.Cmp(expectedCollateral) > 0 {
	//		s := fmt.Sprintf("host expected to post at most %v collateral, but contract has host posting %v: ", expectedCollateral, collateral)
	//		return ExtendErr(s, errLowHostMissedOutput)
	//	}
	//}

	// Check that the revision count has increased.
	if revision.NewRevisionNumber <= oldFCR.NewRevisionNumber {
		return errBadRevisionNumber
	}

	// The Merkle root is checked last because it is the most expensive check.
	if revision.NewFileMerkleRoot != merkle.Sha256CachedTreeRoot(so.SectorRoots, sectorHeight) {
		return errBadFileMerkleRoot
	}

	return nil
}
