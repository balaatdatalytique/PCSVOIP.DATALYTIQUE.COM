// Package db is a thin bbolt wrapper providing JSON-typed Put/Get/Delete/List
// helpers and bucket bootstrapping. The whole admin module persists through it.
package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ErrNotFound is returned by Get when the key does not exist.
var ErrNotFound = errors.New("db: key not found")

// DB wraps a bbolt database. Safe for concurrent use.
type DB struct {
	bolt *bolt.DB
	path string
}

// Open creates parent directories if needed, opens (or creates) the bbolt
// file, and ensures every bucket in allBuckets exists.
func Open(dbPath string) (*DB, error) {
	if dbPath == "" {
		return nil, errors.New("db: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("db: mkdir: %w", err)
	}

	bdb, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}

	if err := bdb.Update(func(tx *bolt.Tx) error {
		for _, name := range allBuckets {
			if _, e := tx.CreateBucketIfNotExists(name); e != nil {
				return fmt.Errorf("create bucket %q: %w", string(name), e)
			}
		}
		return nil
	}); err != nil {
		bdb.Close()
		return nil, err
	}

	return &DB{bolt: bdb, path: dbPath}, nil
}

// Close releases the underlying database.
func (db *DB) Close() error {
	if db == nil || db.bolt == nil {
		return nil
	}
	return db.bolt.Close()
}

// Path returns the absolute database path.
func (db *DB) Path() string { return db.path }

// Bolt exposes the raw bolt.DB for callers that need transactions.
func (db *DB) Bolt() *bolt.DB { return db.bolt }

// Put marshals value as JSON and stores it.
func (db *DB) Put(bucket []byte, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return db.bolt.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %q missing", string(bucket))
		}
		return b.Put([]byte(key), data)
	})
}

// PutRaw stores raw bytes.
func (db *DB) PutRaw(bucket []byte, key string, value []byte) error {
	return db.bolt.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %q missing", string(bucket))
		}
		return b.Put([]byte(key), value)
	})
}

// Get loads JSON into dest. Returns ErrNotFound if the key is missing.
func (db *DB) Get(bucket []byte, key string, dest any) error {
	return db.bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %q missing", string(bucket))
		}
		raw := b.Get([]byte(key))
		if raw == nil {
			return ErrNotFound
		}
		return json.Unmarshal(raw, dest)
	})
}

// GetRaw returns the raw bytes for a key, or ErrNotFound.
func (db *DB) GetRaw(bucket []byte, key string) ([]byte, error) {
	var out []byte
	err := db.bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %q missing", string(bucket))
		}
		raw := b.Get([]byte(key))
		if raw == nil {
			return ErrNotFound
		}
		out = make([]byte, len(raw))
		copy(out, raw)
		return nil
	})
	return out, err
}

// Exists returns true if the key is present.
func (db *DB) Exists(bucket []byte, key string) (bool, error) {
	found := false
	err := db.bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %q missing", string(bucket))
		}
		found = b.Get([]byte(key)) != nil
		return nil
	})
	return found, err
}

// Delete removes a key. Missing keys are not an error.
func (db *DB) Delete(bucket []byte, key string) error {
	return db.bolt.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %q missing", string(bucket))
		}
		return b.Delete([]byte(key))
	})
}

// Count returns the number of entries in a bucket.
func (db *DB) Count(bucket []byte) (int, error) {
	n := 0
	err := db.bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %q missing", string(bucket))
		}
		n = b.Stats().KeyN
		return nil
	})
	return n, err
}

// ForEach iterates a bucket in key order. Stop iteration by returning a non-nil error.
func (db *DB) ForEach(bucket []byte, fn func(key string, raw []byte) error) error {
	return db.bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %q missing", string(bucket))
		}
		return b.ForEach(func(k, v []byte) error {
			return fn(string(k), v)
		})
	})
}

// ForEachReverse iterates a bucket in reverse key order. Useful for "newest first"
// listings on time-ordered (ULID-keyed) buckets like events.
func (db *DB) ForEachReverse(bucket []byte, limit int, fn func(key string, raw []byte) error) error {
	return db.bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %q missing", string(bucket))
		}
		c := b.Cursor()
		count := 0
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			if err := fn(string(k), v); err != nil {
				return err
			}
			count++
			if limit > 0 && count >= limit {
				return nil
			}
		}
		return nil
	})
}
