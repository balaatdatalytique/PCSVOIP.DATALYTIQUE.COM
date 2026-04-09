package admin

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// API handles internal endpoints used by the voice-proxy service.
type API struct {
	Bot      *BotRepo
	KB       *KBRepo
	Visitors *VisitorRepo
	Token    string // INTERNAL_API_TOKEN
}

// NewAPI builds an API handler set.
func NewAPI(bot *BotRepo, kb *KBRepo, visitors *VisitorRepo, token string) *API {
	return &API{Bot: bot, KB: kb, Visitors: visitors, Token: token}
}

// requireToken enforces the X-Internal-Token header. Empty token disables the
// check entirely (useful for local dev). In production set INTERNAL_API_TOKEN.
func (a *API) requireToken(w http.ResponseWriter, r *http.Request) bool {
	if a.Token == "" {
		return true
	}
	got := r.Header.Get("X-Internal-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(a.Token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// Context returns the composed system prompt + active KB as text/plain.
// GET /api/bot/context
func (a *API) Context(w http.ResponseWriter, r *http.Request) {
	if !a.requireToken(w, r) {
		return
	}
	cfg, err := a.Bot.Get()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if cfg == nil || !cfg.Enabled {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Bot-Enabled", "false")
		w.Write([]byte("The assistant is currently unavailable. Please call 844-PCS-VOIP for help."))
		return
	}
	ctx, err := ComposeContext(cfg, a.KB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Bot-Enabled", "true")
	if cfg.Greeting != "" {
		w.Header().Set("X-Bot-Greeting", cfg.Greeting)
	}
	if cfg.Voice != "" {
		w.Header().Set("X-Bot-Voice", cfg.Voice)
	}
	w.Write([]byte(ctx))
}

// VisitorLog records a visitor event posted by voice-proxy.
// POST /api/visitors/log
func (a *API) VisitorLog(w http.ResponseWriter, r *http.Request) {
	if !a.requireToken(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Type    string `json:"type"`
		IP      string `json:"ip"`
		UA      string `json:"ua"`
		Path    string `json:"path"`
		Message string `json:"message"`
		Reply   string `json:"reply"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if in.Type == "" {
		in.Type = "bot_chat"
	}
	if in.IP == "" {
		in.IP = clientIP(r)
	}
	ev := VisitorEvent{
		VisitorID: VisitorID(in.IP, in.UA),
		Type:      in.Type,
		Path:      in.Path,
		Message:   in.Message,
		Reply:     in.Reply,
		IP:        in.IP,
	}
	if err := a.Visitors.Track(ev); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}
