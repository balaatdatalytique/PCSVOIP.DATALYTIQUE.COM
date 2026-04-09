// Package cryptox provides AES-256-GCM encryption helpers and a master-key
// loader. The master key is loaded from ADMIN_MASTER_KEY (64 hex chars). If
// missing on first run, a fresh key is generated and persisted to .env.
package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)


const (
	keyLen      = 32 // AES-256
	nonceLen    = 12 // GCM standard
	envKeyName  = "ADMIN_MASTER_KEY"
)

var (
	once       sync.Once
	masterKey  []byte
	loadErr    error
)

// LoadMasterKey resolves the master key in this order:
//  1. ADMIN_MASTER_KEY env var (64 hex chars)
//  2. {keyDir}/master.key file (created on first run when env is empty)
//  3. Generate a fresh key, write it to {keyDir}/master.key, and log loudly.
//
// keyDir is typically the bbolt data directory so the key persists in the same
// volume as the encrypted data it protects.
func LoadMasterKey(keyDir string) ([]byte, error) {
	once.Do(func() {
		// 1. Env var.
		if hexKey := os.Getenv(envKeyName); hexKey != "" {
			k, err := hex.DecodeString(hexKey)
			if err != nil {
				loadErr = fmt.Errorf("cryptox: %s is not valid hex: %w", envKeyName, err)
				return
			}
			if len(k) != keyLen {
				loadErr = fmt.Errorf("cryptox: %s must be %d bytes (%d hex chars), got %d", envKeyName, keyLen, keyLen*2, len(k))
				return
			}
			masterKey = k
			return
		}

		// 2. Key file in data dir.
		if err := os.MkdirAll(keyDir, 0o755); err != nil {
			loadErr = fmt.Errorf("cryptox: mkdir %s: %w", keyDir, err)
			return
		}
		keyPath := filepath.Join(keyDir, "master.key")
		if data, err := os.ReadFile(keyPath); err == nil {
			hexKey := strings.TrimSpace(string(data))
			k, err := hex.DecodeString(hexKey)
			if err != nil || len(k) != keyLen {
				loadErr = fmt.Errorf("cryptox: %s contains invalid key", keyPath)
				return
			}
			masterKey = k
			return
		}

		// 3. Generate a fresh key.
		k := make([]byte, keyLen)
		if _, err := rand.Read(k); err != nil {
			loadErr = fmt.Errorf("cryptox: rand: %w", err)
			return
		}
		hexEncoded := hex.EncodeToString(k)
		if err := os.WriteFile(keyPath, []byte(hexEncoded+"\n"), 0o600); err != nil {
			log.Printf("cryptox: WARNING — failed to persist master key to %s: %v", keyPath, err)
			log.Printf("cryptox: !!! GENERATED MASTER KEY (SAVE THIS NOW): %s=%s", envKeyName, hexEncoded)
		} else {
			log.Printf("cryptox: generated new master key at %s — back up the data volume; losing this key makes encrypted settings unrecoverable", keyPath)
		}
		masterKey = k
	})
	return masterKey, loadErr
}

// MustLoadMasterKey panics on error — call it only from server bootstrap.
func MustLoadMasterKey(envDir string) []byte {
	k, err := LoadMasterKey(envDir)
	if err != nil {
		log.Fatalf("cryptox: %v", err)
	}
	return k
}

// Encrypt seals plaintext under the master key with AES-256-GCM. The output is
// nonce || ciphertext+tag.
func Encrypt(plaintext []byte) ([]byte, error) {
	if len(masterKey) != keyLen {
		return nil, errors.New("cryptox: master key not initialised; call LoadMasterKey first")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("cryptox: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cryptox: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("cryptox: nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// Decrypt opens nonce || ciphertext+tag back into plaintext.
func Decrypt(box []byte) ([]byte, error) {
	if len(masterKey) != keyLen {
		return nil, errors.New("cryptox: master key not initialised; call LoadMasterKey first")
	}
	if len(box) < nonceLen+16 {
		return nil, errors.New("cryptox: ciphertext too short")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("cryptox: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cryptox: gcm: %w", err)
	}
	nonce, ct := box[:nonceLen], box[nonceLen:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("cryptox: open: %w", err)
	}
	return pt, nil
}

