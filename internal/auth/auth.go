// Package auth handles admin authentication and session management. Sessions
// are persisted in bbolt so they survive container restarts; the in-memory
// fast-path is dropped in favour of a single source of truth.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pcsvoip-cms/internal/db"
)

// Session is the persisted session record.
type Session struct {
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Manager wraps a bbolt-backed session store. It also keeps the bcrypt admin
// hash so the existing handler-level Authenticate API still works.
type Manager struct {
	DB         *db.DB
	adminUser  string
	adminHash  string
	sessionTTL time.Duration
}

// NewManager builds a Manager. database may be nil if the caller has not yet
// migrated to bbolt — in that case sessions are not persistable and the
// returned Manager will fail at session writes. All current call-sites pass a
// non-nil DB.
func NewManager(database *db.DB, adminUser, adminPassHash string) *Manager {
	m := &Manager{
		DB:         database,
		adminUser:  adminUser,
		adminHash:  adminPassHash,
		sessionTTL: 24 * time.Hour,
	}
	go m.cleanupLoop()
	return m
}

// Authenticate validates the supplied credentials against the bootstrapped
// admin. Kept for compatibility — the routes layer still calls this directly.
func (m *Manager) Authenticate(username, password string) bool {
	if username != m.adminUser {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(m.adminHash), []byte(password)) == nil
}

// CreateSession persists a new session and returns the opaque token.
func (m *Manager) CreateSession(userID string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	s := Session{
		UserID:    userID,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(m.sessionTTL),
	}
	if err := m.DB.Put(db.BucketSessions, token, s); err != nil {
		return "", err
	}
	return token, nil
}

// ValidateSession looks up a token and returns the session if still valid.
func (m *Manager) ValidateSession(token string) (*Session, bool) {
	if token == "" {
		return nil, false
	}
	var s Session
	if err := m.DB.Get(db.BucketSessions, token, &s); err != nil {
		return nil, false
	}
	if time.Now().After(s.ExpiresAt) {
		_ = m.DB.Delete(db.BucketSessions, token)
		return nil, false
	}
	return &s, true
}

// DestroySession removes a session from storage.
func (m *Manager) DestroySession(token string) {
	if token == "" {
		return
	}
	_ = m.DB.Delete(db.BucketSessions, token)
}

// GetSessionToken extracts the session cookie value from the request.
func (m *Manager) GetSessionToken(r *http.Request) string {
	cookie, err := r.Cookie("cms_session")
	if err != nil {
		return ""
	}
	return cookie.Value
}

// SessionTTL exposes the configured TTL so handlers can set matching cookies.
func (m *Manager) SessionTTL() time.Duration { return m.sessionTTL }

// cleanupLoop periodically deletes expired sessions.
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.cleanup()
	}
}

func (m *Manager) cleanup() {
	if m.DB == nil {
		return
	}
	now := time.Now()
	_ = m.DB.ForEach(db.BucketSessions, func(k string, raw []byte) error {
		var s Session
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil
		}
		if now.After(s.ExpiresAt) {
			_ = m.DB.Delete(db.BucketSessions, k)
		}
		return nil
	})
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ErrNoSession is returned when no valid session is found.
var ErrNoSession = errors.New("auth: no valid session")
