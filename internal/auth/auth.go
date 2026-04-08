package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Session struct {
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Manager struct {
	sessions   map[string]*Session
	mu         sync.RWMutex
	adminUser  string
	adminHash  string
	sessionTTL time.Duration
}

func NewManager(adminUser, adminPassHash string) *Manager {
	m := &Manager{
		sessions:   make(map[string]*Session),
		adminUser:  adminUser,
		adminHash:  adminPassHash,
		sessionTTL: 24 * time.Hour,
	}
	go m.cleanupLoop()
	return m
}

func (m *Manager) Authenticate(username, password string) bool {
	if username != m.adminUser {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(m.adminHash), []byte(password))
	return err == nil
}

func (m *Manager) CreateSession(userID string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	m.sessions[token] = &Session{
		UserID:    userID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(m.sessionTTL),
	}
	m.mu.Unlock()

	return token, nil
}

func (m *Manager) ValidateSession(token string) (*Session, bool) {
	m.mu.RLock()
	sess, ok := m.sessions[token]
	m.mu.RUnlock()

	if !ok || time.Now().After(sess.ExpiresAt) {
		if ok {
			m.DestroySession(token)
		}
		return nil, false
	}
	return sess, true
}

func (m *Manager) DestroySession(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

func (m *Manager) GetSessionToken(r *http.Request) string {
	cookie, err := r.Cookie("cms_session")
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(15 * time.Minute)
	for range ticker.C {
		m.mu.Lock()
		for k, s := range m.sessions {
			if time.Now().After(s.ExpiresAt) {
				delete(m.sessions, k)
			}
		}
		m.mu.Unlock()
	}
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
