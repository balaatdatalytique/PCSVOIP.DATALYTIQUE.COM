package admin

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// We don't pull a ULID dependency for one helper. NewID returns a 26-char
// lexicographically-sortable identifier with millisecond precision: 13 hex chars
// of timestamp + 13 hex chars of randomness. Sorts by time, unique enough.
var idMu sync.Mutex

func NewID() string {
	idMu.Lock()
	defer idMu.Unlock()
	ts := time.Now().UTC().UnixMilli()
	tsHex := make([]byte, 13)
	// Hex of 52 bits (13 hex chars) covers ~142 years from 1970, plenty.
	for i := 12; i >= 0; i-- {
		tsHex[i] = "0123456789abcdef"[ts&0xf]
		ts >>= 4
	}
	rnd := make([]byte, 7)
	_, _ = rand.Read(rnd)
	return string(tsHex) + hex.EncodeToString(rnd)[:13]
}

// VisitorID derives a stable per-visitor identifier from IP + User-Agent. Not a
// privacy guarantee — just enough to roll up repeat visits in the dashboard.
func VisitorID(ip, ua string) string {
	h := sha1Sum(ip + "|" + ua)
	return hex.EncodeToString(h[:8])
}
