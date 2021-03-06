// Copyright 2019 DxChain, All rights reserved.
// Use of this source code is governed by an Apache
// License 2.0 that can be found in the LICENSE file

package contractmanager

import (
	"errors"
	"fmt"
	"github.com/DxChainNetwork/godx/storage"
	"reflect"
)

// SetRentPayment will set the rent payment to the value passed in by the user
// through the command line interface
func (cm *ContractManager) SetRentPayment(rent storage.RentPayment) (err error) {
	if cm.b.Syncing() {
		return errors.New("setRentPayment can only be done once the block chain finished syncing")
	}

	// validate the rentPayment, making sure that fields are not empty
	if err = RentPaymentValidation(rent); err != nil {
		return
	}

	// getting the old payment
	cm.lock.Lock()
	oldCurrentPeriod := cm.currentPeriod
	oldRent := cm.rentPayment
	cm.rentPayment = rent
	cm.lock.Unlock()

	// if error is not nil, revert the settings back to the
	// original settings
	defer func() {
		if err != nil {
			cm.lock.Lock()
			cm.rentPayment = oldRent
			cm.currentPeriod = oldCurrentPeriod
			cm.lock.Unlock()
		}
	}()

	// indicates the contracts have been canceled previously
	// or it is client's first time signing the storage contract
	if reflect.DeepEqual(oldRent, storage.RentPayment{}) {
		// update the current period
		cm.lock.Lock()
		cm.currentPeriod = cm.blockHeight - rent.RenewWindow
		cm.lock.Unlock()

		// reuse the active canceled contracts
		if err = cm.resumeContracts(); err != nil {
			cm.log.Error("SetRentPayment failed, error resuming the active storage contracts", "err", err.Error())
			return
		}
	}

	// update storage host manager rentPayment payment
	// which updates the storage host evaluation function
	if err = cm.hostManager.SetRentPayment(rent); err != nil {
		cm.log.Error("SetRentPayment failed, failed to set the rent payment for host manager", "err", err.Error())
		return
	}

	// save all the settings
	if err = cm.saveSettings(); err != nil {
		cm.log.Error("SetRentPayment failed, unable to save settings while setting the rent payment", "err", err.Error())
		// set the storage host's rentPayment back to original value
		_ = cm.hostManager.SetRentPayment(oldRent)
		return fmt.Errorf("failed to save settings persistently: %s", err.Error())
	}

	// if the maintenance process is running, stop it
	cm.lock.Lock()
	if cm.maintenanceRunning {
		cm.maintenanceStop <- struct{}{}
	}
	cm.lock.Unlock()

	// wait util the current maintenance finished execution, and start new maintenance
	go func() {
		cm.maintenanceWg.Wait()
		cm.contractMaintenance()
	}()

	return
}

// AcquireRentPayment will return the RentPayment settings
func (cm *ContractManager) AcquireRentPayment() (rentPayment storage.RentPayment) {
	return cm.rentPayment
}

// RentPaymentValidation will validate the rentPayment. All fields must be
// non-zero value
func RentPaymentValidation(rent storage.RentPayment) (err error) {
	switch {
	case rent.StorageHosts == 0:
		return errors.New("amount of storage hosts cannot be set to 0")
	case rent.Period == 0:
		return errors.New("storage period cannot be set to 0")
	case rent.RenewWindow == 0:
		return errors.New("renew window cannot be set to 0")
	case rent.ExpectedStorage == 0:
		return errors.New("expected storage cannot be set to 0")
	case rent.ExpectedUpload == 0:
		return errors.New("expectedUpload cannot be set to 0")
	case rent.ExpectedDownload == 0:
		return errors.New("expectedDownload cannot be set to 0")
	case rent.ExpectedRedundancy == 0:
		return errors.New("expectedRedundancy cannot be set to 0")
	case rent.RenewWindow > rent.Period:
		return errors.New("renew window cannot be larger than the period")
	default:
		return
	}
}
