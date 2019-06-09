// Copyright 2019 DxChain, All rights reserved.
// Use of this source code is governed by an Apache
// License 2.0 that can be found in the LICENSE file.

package newstoragemanager

const (
	// database related keys and prefixes
	prefixFolder         = "storagefolder"
	prefixFolderPathToID = "folderPathToID"

	sectorSaltKey = "sectorSalt"
)

const (
	databaseFileName = "storagemanager.db"
	walFileName      = "storagemanager.wal"
	dataFileName     = "dxstorage.dat"
)

const (
	// target is the process target
	// targetNormal is the normal execution of an update
	targetNormal uint8 = iota

	// targetRecoverCommitted is the state of recovering a committed transaction
	targetRecoverCommitted

	// targetRecoverUncommitted is the state of recovering an uncommitted trnasaction
	targetRecoverUncommitted
)

const (
	folderAvailable uint32 = iota
	folderUnavailable
)

const (
	// maxSectorsPerFolder defines the maximum number of sectors in a folder
	maxSectorsPerFolder uint64 = 1 << 32

	// minSectorsPerFolder defines the minimum number of sectors in a folder
	minSectorsPerFolder uint64 = 1 << 3

	// maxNumFolders defines the maximum number of storage folders
	maxNumFolders = 1 << 16
)
