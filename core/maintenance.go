// Copyright 2019 DxChain, All rights reserved.
// Use of this source code is governed by an Apache
// License 2.0 that can be found in the LICENSE file.

package core

import (
	"errors"
	"strconv"
	"sync"

	"github.com/DxChainNetwork/godx/common"
	"github.com/DxChainNetwork/godx/core/state"
	"github.com/DxChainNetwork/godx/core/types"
	"github.com/DxChainNetwork/godx/core/vm"
	"github.com/DxChainNetwork/godx/ethdb"
	"github.com/DxChainNetwork/godx/event"
	"github.com/DxChainNetwork/godx/log"

	"github.com/syndtr/goleveldb/leveldb"
)

const (
	// canonicalChainEvChanSize is the size of channel listening to CanonicalChainHeadEvent.
	canonicalChainEvChanHeadSize = 10
)

var (
	emptyStorageContractID = common.Hash{}

	errStorageProofTiming     = errors.New("missed proof triggered for file contract that is not expiring")
	errMissingStorageContract = errors.New("storage proof submitted for non existing file contract")
)

// Backend wraps all methods required for maintenance.
type Backend interface {
	SubscribeCanonicalChainEvent(ch chan<- CanonicalChainHeadEvent) event.Subscription
	ChainDb() ethdb.Database
}

type MaintenanceSystem struct {
	backend Backend
	state   *state.StateDB

	// Subscription for new canonical chain event
	canonicalChainSub event.Subscription

	// Channel to receive new canonical chain event
	canonicalChainCh chan CanonicalChainHeadEvent
	quitCh           chan struct{}
	wg               sync.WaitGroup
}

func NewMaintenanceSystem(backend Backend, state *state.StateDB) *MaintenanceSystem {
	m := &MaintenanceSystem{
		backend:          backend,
		state:            state,
		canonicalChainCh: make(chan CanonicalChainHeadEvent, canonicalChainEvChanHeadSize),
		quitCh:           make(chan struct{}),
	}

	// subscribe canonical chain head event for file contract maintenance
	m.canonicalChainSub = m.backend.SubscribeCanonicalChainEvent(m.canonicalChainCh)
	m.wg.Add(1)
	go m.maintenanceLoop()
	return m
}

func (m *MaintenanceSystem) maintenanceLoop() {
	defer m.wg.Done()

	for {
		select {
		case ev := <-m.canonicalChainCh:
			db := m.backend.ChainDb()
			err := m.applyMaintenance(db, ev.Block)
			if err != nil {
				log.Error("failed to apply maintenace", "error", err)
				return
			}
		case err := <-m.canonicalChainSub.Err():
			log.Error("failed to subscribe canonical chain head event for file contract maintenance", "error", err)
			return
		case <-m.quitCh:
			log.Info("maintenance stopped")
			return
		}
	}
}

func (m *MaintenanceSystem) Stop() {
	log.Info("maintenance stopping ...")
	m.canonicalChainSub.Unsubscribe()
	close(m.quitCh)
	m.wg.Wait()
}

func (m *MaintenanceSystem) applyMissedStorageProof(db ethdb.Database, height uint64, fcid common.Hash) error {

	// check if fileContract of this fcid exists
	fc, err := vm.GetStorageContract(db, fcid)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return errMissingStorageContract
		}
		return err
	}

	// Check that whether the file contract is in expire bucket at block height.
	if fc.WindowEnd != height {
		return errStorageProofTiming
	}

	// Remove the file contract from db
	vm.DeleteStorageContract(db, fcid)
	vm.DeleteExpireStorageContract(db, fcid, height)

	// effect the missed proof output
	mpos := fc.MissedProofOutputs
	for _, mpo := range mpos {
		m.state.AddBalance(mpo.Address, mpo.Value)
	}
	m.state.Finalise(true)

	return nil
}

// applyStorageContractMaintenance looks for all of the file contracts that have
// expired without an appropriate storage proof, and calls 'applyMissedProof'
// for the file contract.
func (m *MaintenanceSystem) applyStorageContractMaintenance(db ethdb.Database, block *types.Block) error {
	ldb, ok := db.(*ethdb.LDBDatabase)
	if !ok {
		return errors.New("not persistent db")
	}
	blockHeight := block.NumberU64()
	heightStr := strconv.FormatUint(blockHeight, 10)
	iterator := ldb.NewIteratorWithPrefix([]byte(vm.PrefixExpireStorageContract + heightStr + "-"))

	for iterator.Next() {
		keyBytes := iterator.Key()
		height, fcID := vm.SplitStorageContractID(keyBytes)
		if height == 0 && fcID == emptyStorageContractID {
			log.Warn("split empty file contract ID")
			continue
		}
		err := m.applyMissedStorageProof(db, uint64(height), fcID)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *MaintenanceSystem) applyMaintenance(db ethdb.Database, block *types.Block) error {
	err := m.applyStorageContractMaintenance(db, block)
	if err != nil {
		return err
	}

	return nil
}