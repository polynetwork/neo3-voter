package db

import (
	"encoding/binary"
	"fmt"
	"github.com/boltdb/bolt"

	"path"
	"strings"
	"sync"
)

var (
	BKTHeight     = []byte("Height")
	PolyHeightKey = []byte("Poly")
	NeoHeightKey  = []byte("Neo")
)

type BoltDB struct {
	rwLock   *sync.RWMutex
	db       *bolt.DB
	filePath string
}

func NewBoltDB(filePath string) (*BoltDB, error) {
	if filePath == "" {
		return nil, fmt.Errorf("db path is empty")
	}
	if !strings.Contains(filePath, ".bin") {
		filePath = path.Join(filePath, "bolt.bin")
	}
	w := new(BoltDB)
	db, err := bolt.Open(filePath, 0644, &bolt.Options{InitialMmapSize: 500000})
	if err != nil {
		return nil, err
	}
	w.db = db
	w.rwLock = new(sync.RWMutex)
	w.filePath = filePath
	// height
	if err = db.Update(func(btx *bolt.Tx) error {
		_, err := btx.CreateBucketIfNotExists(BKTHeight)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return w, nil
}

func (w *BoltDB) PutZionHeight(height uint64) error {
	w.rwLock.Lock()
	defer w.rwLock.Unlock()

	raw := make([]byte, 8)
	binary.LittleEndian.PutUint64(raw, height)
	return w.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(BKTHeight)
		err := bucket.Put(PolyHeightKey, raw)
		if err != nil {
			return err
		}

		return nil
	})
}

func (w *BoltDB) GetZionHeight() uint64 {
	w.rwLock.RLock()
	defer w.rwLock.RUnlock()

	var height uint64
	_ = w.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(BKTHeight)
		raw := bucket.Get(PolyHeightKey)
		if len(raw) == 0 {
			height = 0
			return nil
		}
		height = binary.LittleEndian.Uint64(raw)
		return nil
	})

	return height
}

func (w *BoltDB) PutNeoHeight(height uint64) error {
	w.rwLock.Lock()
	defer w.rwLock.Unlock()

	raw := make([]byte, 8)
	binary.LittleEndian.PutUint64(raw, height)
	return w.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(BKTHeight)
		err := bucket.Put(NeoHeightKey, raw)
		if err != nil {
			return err
		}

		return nil
	})
}

func (w *BoltDB) GetNeoHeight() uint64 {
	w.rwLock.RLock()
	defer w.rwLock.RUnlock()

	var height uint64
	_ = w.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(BKTHeight)
		raw := bucket.Get(NeoHeightKey)
		if len(raw) == 0 {
			height = 0
			return nil
		}
		height = binary.LittleEndian.Uint64(raw)
		return nil
	})

	return height
}

func (w *BoltDB) Close() {
	w.rwLock.Lock()
	w.db.Close()
	w.rwLock.Unlock()
}
