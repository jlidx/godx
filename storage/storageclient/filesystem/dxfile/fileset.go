// Copyright 2019 DxChain, All rights reserved.
// Use of this source code is governed by an Apache
// License 2.0 that can be found in the LICENSE file.

package dxfile

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"github.com/DxChainNetwork/godx/common/writeaheadlog"
	"github.com/DxChainNetwork/godx/crypto"
	"github.com/DxChainNetwork/godx/storage/storageclient/erasurecode"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const threadDepth = 3

var (
	// ErrUnknownFile is the error for opening a file that not exists on disk
	ErrUnknownFile = errors.New("file not known")

	// ErrFileExist is the error for creating a new file while there already exist a file
	// with the same name
	ErrFileExist = errors.New("file already exist")
)

type (
	// FileSet is the set of DxFile
	FileSet struct {
		// filesDir is the file directory for dxfile
		filesDir string

		// filesMap is the mapping from dxPath to contents
		filesMap map[string]*fileSetEntry

		lock sync.Mutex
		wal  *writeaheadlog.Wal
	}

	// fileSetEntry is an entry for fileSet. fileSetEntry extends DxFile.
	// fileSetEntry also keeps a threadMap that traces all threads using the DxFile
	// fileSetEntry is released in FileSet only if all threads in threadMap are all closed.
	fileSetEntry struct {
		*DxFile
		fileSet *FileSet

		threadMap     map[uint64]threadInfo
		threadMapLock sync.Mutex
	}

	// FileSetEntryWithID is a fileSetEntry with the threadID. FileSetEntryWithID extends DxFile
	FileSetEntryWithID struct {
		*fileSetEntry
		threadID uint64
	}

	// threadInfo is the entry in threadMap
	threadInfo struct {
		callingFiles []string
		callingLines []int
		lockTime     time.Time
	}
)

// NewFileSet create a new DxFileSet with provided filesDir and wal.
func NewFileSet(filesDir string, wal *writeaheadlog.Wal) *FileSet {
	return &FileSet{
		filesDir: filesDir,
		filesMap: make(map[string]*fileSetEntry),
		wal:      wal,
	}
}

// NewDxFile create a DxFile based on the params given. Return a FileSetEntryWithID that has been
// registered with threadID in FileSetEntry
func (fs *FileSet) NewDxFile(dxPath string, sourcePath string, force bool, erasureCode erasurecode.ErasureCoder, cipherKey crypto.CipherKey, fileSize uint64, fileMode os.FileMode) (*FileSetEntryWithID, error) {
	fs.lock.Lock()
	defer fs.lock.Unlock()
	exists := fs.exists(dxPath)
	if exists && !force {
		return nil, ErrFileExist
	}
	// Create a new DxFile
	filePath := filepath.Join(fs.filesDir, dxPath)
	df, err := New(filePath, dxPath, sourcePath, fs.wal, erasureCode, cipherKey, fileSize, fileMode)
	if err != nil {
		return nil, err
	}
	// Assign a threadID to the new DxFile. Register the threadID to the entry.
	entry := fs.newFileSetEntry(df)
	threadID := randomThreadID()
	entry.threadMap[threadID] = newThreadInfo()
	fs.filesMap[dxPath] = entry
	return &FileSetEntryWithID{
		fileSetEntry: entry,
		threadID:     threadID,
	}, nil
}

// CopyEntry copy the FileSetEntry. A new thread is created and registered in entry.threadMap
func (entry *FileSetEntryWithID) CopyEntry() *FileSetEntryWithID {
	entry.threadMapLock.Lock()
	defer entry.threadMapLock.Unlock()

	threadUID := randomThreadID()
	copied := &FileSetEntryWithID{
		fileSetEntry: entry.fileSetEntry,
		threadID:     threadUID,
	}
	entry.threadMap[threadUID] = newThreadInfo()
	return copied
}

// Open open a DxFile with dxPath, return the FileSetEntry, along with the threadID
func (fs *FileSet) Open(dxPath string) (*FileSetEntryWithID, error) {
	fs.lock.Lock()
	defer fs.lock.Unlock()
	return fs.open(dxPath)
}

// open is the helper function for open the DxFile specified by input dxPath.
// return an entry with registered with id.
func (fs *FileSet) open(dxPath string) (*FileSetEntryWithID, error) {
	entry, exist := fs.filesMap[dxPath]
	if !exist {
		// file not loaded or not exist. Try to read DxFile from disk.
		df, err := readDxFile(filepath.Join(fs.filesDir, dxPath), fs.wal)
		if os.IsNotExist(err) {
			return nil, ErrUnknownFile
		}
		if err != nil {
			return nil, err
		}
		entry = fs.newFileSetEntry(df)
		fs.filesMap[dxPath] = entry
	}
	if entry.Deleted() {
		return nil, ErrUnknownFile
	}
	// Register the threadID
	threadID := randomThreadID()
	entry.threadMapLock.Lock()
	defer entry.threadMapLock.Unlock()
	entry.threadMap[threadID] = newThreadInfo()
	return &FileSetEntryWithID{
		fileSetEntry: entry,
		threadID:     threadID,
	}, nil
}

// Delete delete a file with dxPath from the file set. Also the DxFile specified by dxPath on disk is also deleted
func (fs *FileSet) Delete(dxPath string) error {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	entry, err := fs.open(dxPath)
	if err != nil {
		return err
	}
	defer fs.closeEntry(entry)
	err = entry.Delete()
	if err != nil {
		return err
	}

	delete(fs.filesMap, entry.metadata.DxPath)
	return nil
}

// Exists is the public function that returns whether the dxPath exists (cached then on disk)
func (fs *FileSet) Exists(dxPath string) bool {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	return fs.exists(dxPath)
}

// Exists return whether the dxPath exists (cached then on disk)
func (fs *FileSet) exists(dxPath string) bool {
	entry, exists := fs.filesMap[dxPath]
	if exists {
		return !entry.Deleted()
	}
	_, err := os.Stat(filepath.Join(fs.filesDir, dxPath))
	return !os.IsNotExist(err)
}

// Rename rename the file with dxPath to newDxPath.
func (fs *FileSet) Rename(dxPath, newDxPath string) error {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	exists := fs.exists(newDxPath)
	if exists {
		return ErrFileExist
	}
	entry, err := fs.open(dxPath)
	if err != nil {
		return err
	}
	defer fs.closeEntry(entry)

	fs.filesMap[newDxPath] = entry.fileSetEntry
	delete(fs.filesMap, dxPath)

	return entry.Rename(newDxPath, filepath.Join(fs.filesDir, newDxPath))
}

// Close close a FileSetEntryWithID
func (entry *FileSetEntryWithID) Close() error {
	entry.fileSet.lock.Lock()
	entry.fileSet.closeEntry(entry)
	entry.fileSet.lock.Unlock()
	return nil
}

// newFileSetEntry is a helper function to create a fileSetEntry based on input df
func (fs *FileSet) newFileSetEntry(df *DxFile) *fileSetEntry {
	return &fileSetEntry{
		DxFile:    df,
		fileSet:   fs,
		threadMap: make(map[uint64]threadInfo),
	}
}

// closeEntry close the entry with id in fileSet
func (fs *FileSet) closeEntry(entry *FileSetEntryWithID) {
	entry.threadMapLock.Lock()
	defer entry.threadMapLock.Unlock()
	delete(entry.threadMap, entry.threadID)

	currentEntry := fs.filesMap[entry.metadata.DxPath]
	if currentEntry != entry.fileSetEntry {
		return
	}
	if len(currentEntry.threadMap) == 0 {
		delete(fs.filesMap, entry.metadata.DxPath)
	}
}

// newThreadinfo created a threadInfo entry for the threadMap
func newThreadInfo() threadInfo {
	ti := threadInfo{
		callingFiles: make([]string, threadDepth+1),
		callingLines: make([]int, threadDepth+1),
		lockTime:     time.Now(),
	}
	for i := 0; i <= threadDepth; i++ {
		_, ti.callingFiles[i], ti.callingLines[i], _ = runtime.Caller(2 + i)
	}
	return ti
}

// randomThreadID create a random thread id
func randomThreadID() uint64 {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		panic("cannot create random thread id")
	}
	return binary.LittleEndian.Uint64(b)
}
