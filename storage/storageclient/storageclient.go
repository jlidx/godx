// Copyright 2019 DxChain, All rights reserved.
// Use of this source code is governed by an Apache
// License 2.0 that can be found in the LICENSE file.

package storageclient

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"path/filepath"
	"sync"

	"github.com/DxChainNetwork/godx/accounts"
	"github.com/DxChainNetwork/godx/common"
	"github.com/DxChainNetwork/godx/common/hexutil"
	"github.com/DxChainNetwork/godx/common/threadmanager"
	"github.com/DxChainNetwork/godx/core/types"
	"github.com/DxChainNetwork/godx/log"
	"github.com/DxChainNetwork/godx/p2p"
	"github.com/DxChainNetwork/godx/params"
	"github.com/DxChainNetwork/godx/rlp"
	"github.com/DxChainNetwork/godx/rpc"
	"github.com/DxChainNetwork/godx/storage"
	"github.com/DxChainNetwork/godx/storage/storageclient/memorymanager"
	"github.com/DxChainNetwork/godx/storage/storageclient/storagehostmanager"
)

var (
	zeroValue = new(big.Int).SetInt64(0)
)

// ************** MOCKING DATA *****************
// *********************************************
type (
	contractManager   struct{}
	StorageContractID struct{}
	StorageHostEntry  struct{}
	streamCache       struct{}
	Wal               struct{}
)

// *********************************************
// *********************************************

// Backend allows Ethereum object to be passed in as interface
type Backend interface {
	APIs() []rpc.API
	AccountManager() *accounts.Manager
	SuggestPrice(ctx context.Context) (*big.Int, error)
	GetPoolNonce(ctx context.Context, addr common.Address) (uint64, error)
	ChainConfig() *params.ChainConfig
	CurrentBlock() *types.Block
	SendTx(ctx context.Context, signedTx *types.Transaction) error
}

// StorageClient contains fileds that are used to perform StorageHost
// selection operation, file uploading, downloading operations, and etc.
type StorageClient struct {
	// TODO (jacky): File Management Related

	// TODO (jacky): File Download Related

	// TODO (jacky): File Upload Related

	// Todo (jacky): File Recovery Related

	// Memory Management
	memoryManager *memorymanager.MemoryManager

	// contract manager and storage host manager
	contractManager    *contractManager
	storageHostManager *storagehostmanager.StorageHostManager

	// TODO (jacky): workerpool

	// Cache the hosts from the last price estimation result
	lastEstimationStorageHost []StorageHostEntry

	// Directories and File related
	persist        persistence
	persistDir     string
	staticFilesDir string

	// Utilities
	streamCache *streamCache
	log         log.Logger
	lock        sync.Mutex
	tm          threadmanager.ThreadManager
	wal         Wal

	// information on network, block chain, and etc.
	info       ParsedAPI
	ethBackend storage.EthBackend
	b          Backend

	// get the P2P server for adding peer
	p2pServer *p2p.Server
}

// New initializes StorageClient object
func New(persistDir string) (*StorageClient, error) {

	// TODO (Jacky): data initialization
	sc := &StorageClient{
		persistDir:     persistDir,
		staticFilesDir: filepath.Join(persistDir, DxPathRoot),
	}

	sc.memoryManager = memorymanager.New(DefaultMaxMemory, sc.tm.StopChan())
	sc.storageHostManager = storagehostmanager.New(sc.persistDir)

	return sc, nil
}

// Start controls go routine checking and updating process
func (sc *StorageClient) Start(b storage.EthBackend, server *p2p.Server) error {
	// get the eth backend
	sc.ethBackend = b

	// validation
	if server == nil {
		return errors.New("failed to get the P2P server")
	}

	// get the p2p server for the adding peers
	sc.p2pServer = server

	// getting all needed API functions
	err := sc.filterAPIs(b.APIs())
	if err != nil {
		return err
	}

	// TODO: (mzhang) Initialize ContractManager & HostManager -> assign to StorageClient
	err = sc.storageHostManager.Start(sc.p2pServer, sc)
	if err != nil {
		return err
	}

	// Load settings from persist file
	if err := sc.loadPersist(); err != nil {
		return err
	}

	// TODO (mzhang): Subscribe consensus change

	// TODO (Jacky): DxFile / DxDirectory Update & Initialize Stream Cache

	// TODO (Jacky): Starting Worker, Checking file healthy, etc.

	// TODO (mzhang): Register On Stop Thread Control Function, waiting for WAL

	return nil
}

func (sc *StorageClient) Close() error {
	err := sc.storageHostManager.Close()
	errSC := sc.tm.Stop()
	return common.ErrCompose(err, errSC)
}

func (sc *StorageClient) setBandwidthLimits(uploadSpeedLimit int64, downloadSpeedLimit int64) error {
	// validation
	if uploadSpeedLimit < 0 || downloadSpeedLimit < 0 {
		return errors.New("upload/download speed limit cannot be negative")
	}

	// Update the contract settings accordingly
	if uploadSpeedLimit == 0 && downloadSpeedLimit == 0 {
		// TODO (mzhang): update contract settings using contract manager
	} else {
		// TODO (mzhang): update contract settings to the loaded data
	}

	return nil
}

func (s *StorageClient) FormContract(params ContractParams) error {
	// Extract vars from params, for convenience.
	allowance, funding, startHeight, endHeight, host := params.Allowance, params.Funding, params.StartHeight, params.EndHeight, params.Host

	// TODO: 以太坊中交易手续费是在交易执行中扣除了，这里不需要考虑交易手续费？？？

	// Calculate the payouts for the renter, host, and whole contract.
	period := endHeight - startHeight
	expectedStorage := allowance.ExpectedStorage / allowance.Hosts
	renterPayout, hostPayout, _, err := RenterPayoutsPreTax(host, funding, zeroValue, zeroValue, period, expectedStorage)
	if err != nil {
		return err
	}

	// TODO: 获取renter的public key.
	renterPubkey := ecdsa.PublicKey{}
	uc := types.UnlockConditions{
		PublicKeys: []ecdsa.PublicKey{
			renterPubkey,
			host.PublicKey,
		},
		SignaturesRequired: 2,
	}

	// TODO: 计算renter和host的账户地址
	renterAddr := common.Address{}
	hostAddr := common.Address{}
	// Create file contract.
	sc := types.StorageContract{
		FileSize:         0,
		FileMerkleRoot:   common.Hash{}, // no proof possible without data
		WindowStart:      endHeight,
		WindowEnd:        endHeight + host.WindowSize,
		RenterCollateral: types.DxcoinCollateral{types.DxcoinCharge{Value: renterPayout}},
		HostCollateral:   types.DxcoinCollateral{types.DxcoinCharge{Value: hostPayout}},
		UnlockHash:       uc.UnlockHash(),
		RevisionNumber:   0,
		ValidProofOutputs: []types.DxcoinCharge{
			// Outputs need to account for tax.
			{Value: renterPayout, Address: renterAddr}, // This is the renter payout, but with tax applied.
			// Collateral is returned to host.
			{Value: hostPayout, Address: hostAddr},
		},
		MissedProofOutputs: []types.DxcoinCharge{
			// Same as above.
			{Value: renterPayout, Address: renterAddr},
			// Same as above.
			{Value: hostPayout, Address: hostAddr},
			// Once we start doing revisions, we'll move some coins to the host and some to the void.
			{Value: zeroValue, Address: common.Address{}},
		},
	}

	scBytes, err := rlp.EncodeToBytes(sc)
	if err != nil {
		return err
	}

	// TODO: 记录与当前host协商交互的结果，用于后续健康度检查
	//defer func() {
	//	if err != nil {
	//		hdb.IncrementFailedInteractions(host.PublicKey)
	//		err = errors.Extend(err, modules.ErrHostFault)
	//	} else {
	//		hdb.IncrementSuccessfulInteractions(host.PublicKey)
	//	}
	//}()

	// setup connection with storage host
	peer, err := s.ethBackend.SetupConnection(host.NetAddress)
	defer s.ethBackend.Disconnect(host.NetAddress)

	// Send the FormContract request.
	req := FormContractRequest{
		StorageContract: sc,
		RenterKey:       uc.PublicKeys[0],
	}

	rlpBytes, err := rlp.EncodeToBytes(req)
	if err != nil {
		return err
	}

	if err := peer.SendStorageContractCreation(rlpBytes); err != nil {
		return err
	}

	// TODO: 等待与host协商完成的通知 channel，然后直接发送 form contract交易就好
	negotiationFinish := make(chan struct{})
	sendAPI := NewStorageContractTxAPI(s.b)
	for {
		select {
		case <-negotiationFinish:
			args := SendStorageContractTxArgs{
				From: renterAddr,
			}
			addr := common.Address{}
			addr.SetBytes([]byte{10})
			args.To = &addr
			args.Input = (*hexutil.Bytes)(&scBytes)
			ctx := context.Background()
			sendAPI.SendFormContractTX(ctx, args)
		default:
		}
	}

	if err != nil {
		return err
	}

	// TODO: 构造这个合约信息.
	//header := contractHeader{
	//	Transaction: revisionTxn,
	//	SecretKey:   ourSK,
	//	StartHeight: startHeight,
	//	TotalCost:   funding,
	//	ContractFee: host.ContractPrice,
	//	TxnFee:      txnFee,
	//	SiafundFee:  types.Tax(startHeight, fc.Payout),
	//	Utility: modules.ContractUtility{
	//		GoodForUpload: true,
	//		GoodForRenew:  true,
	//	},
	//}

	// TODO: 保存这个合约信息到本地
	//meta, err := cs.managedInsertContract(header, nil) // no Merkle roots yet
	//if err != nil {
	//	return RenterContract{}, err
	//}
	return nil
}
