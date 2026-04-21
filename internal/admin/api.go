package admin

import (
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"strings"
)

// API handles internal endpoints used by the voice-proxy service.
type API struct {
	Bot      *BotRepo
	KB       *KBRepo
	Visitors *VisitorRepo
	Settings *SettingsRepo
	Token    string // INTERNAL_API_TOKEN
}

// NewAPI builds an API handler set.
func NewAPI(bot *BotRepo, kb *KBRepo, visitors *VisitorRepo, settings *SettingsRepo, token string) *API {
	return &API{Bot: bot, KB: kb, Visitors: visitors, Settings: settings, Token: token}
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

// OutboundContext returns the outbound callback persona + KB as text/plain.
// GET /api/bot/outbound
func (a *API) OutboundContext(w http.ResponseWriter, r *http.Request) {
	if !a.requireToken(w, r) {
		return
	}
	cfg, err := a.Bot.GetByKey("outbound_aria")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if cfg == nil || !cfg.Enabled {
		// Fall back to the default bot
		a.Context(w, r)
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

// QuoteSubmit handles POST /api/quote — sends the quote form as an email
// using the SMTP settings from the admin module.
func (a *API) QuoteSubmit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	// FormData from browser sends multipart/form-data.
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		// Fall back to url-encoded (e.g. curl).
		if err2 := r.ParseForm(); err2 != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid form data"})
			return
		}
	}

	firstName := strings.TrimSpace(r.PostFormValue("first_name"))
	lastName := strings.TrimSpace(r.PostFormValue("last_name"))
	email := strings.TrimSpace(r.PostFormValue("email"))
	phone := strings.TrimSpace(r.PostFormValue("phone"))
	business := strings.TrimSpace(r.PostFormValue("business"))
	numPhones := strings.TrimSpace(r.PostFormValue("num_phones"))
	numLocations := strings.TrimSpace(r.PostFormValue("num_locations"))
	products := r.Form["products"]

	if firstName == "" || lastName == "" || email == "" || phone == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Please fill in all required fields (name, email, phone)."})
		return
	}

	// Build product list
	productList := "None selected"
	if len(products) > 0 {
		productList = strings.Join(products, ", ")
	}

	// Load SMTP settings
	s, err := a.Settings.Get()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Email service unavailable"})
		return
	}
	if s.SMTPHost == "" || s.SMTPPort == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Email service not configured"})
		return
	}

	pass, err := s.SMTPPass.Open()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Email service unavailable"})
		return
	}

	from := s.SMTPFromEmail
	if from == "" {
		from = s.SMTPUser
	}
	fromName := s.SMTPFromName
	if fromName == "" {
		fromName = "PCS VoIP Website"
	}

	// Determine recipient — admin email from settings, fallback to SMTP user
	to := s.AdminEmail
	if to == "" {
		to = s.SMTPUser
	}

	subject := "New Quote Request from " + firstName + " " + lastName

	body := fmt.Sprintf(`New quote request from PCS VoIP website:

Name: %s %s
Email: %s
Phone: %s
Business: %s
Number of Phones: %s
Number of Locations: %s

Products/Services Interested In:
%s
`,
		firstName, lastName, email, phone, business, numPhones, numLocations, productList)

	msg := []byte("To: " + to + "\r\n" +
		"From: " + fromName + " <" + from + ">\r\n" +
		"Reply-To: " + email + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" + body)

	if err := sendSMTP(s.SMTPHost, s.SMTPPort, s.SMTPUser, pass, from, []string{to}, msg); err != nil {
		log.Printf("quote email error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to send email. Please try again or call us at 844-PCS-VOIP."})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"ok": "Thank you! Your quote request has been submitted. Our team will contact you shortly."})
}

// sendSMTP sends an email handling both port 465 (implicit TLS/SMTPS) and
// port 587 (STARTTLS). Go's net/smtp.SendMail only supports STARTTLS.
func sendSMTP(host string, port int, user, pass, from string, to []string, msg []byte) error {
	addr := fmt.Sprintf("%s:%d", host, port)

	if port == 465 {
		// Implicit TLS — dial with TLS directly.
		tlsCfg := &tls.Config{ServerName: host}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("tls dial: %w", err)
		}
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("smtp client: %w", err)
		}
		defer c.Close()

		// Use LOGIN auth — common on cPanel/HostMonster shared hosting.
		// PLAIN often fails on these servers.
		if err := c.Auth(&loginAuth{user, pass}); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
		if err := c.Mail(from); err != nil {
			return fmt.Errorf("smtp mail: %w", err)
		}
		for _, rcpt := range to {
			if err := c.Rcpt(rcpt); err != nil {
				return fmt.Errorf("smtp rcpt: %w", err)
			}
		}
		w, err := c.Data()
		if err != nil {
			return fmt.Errorf("smtp data: %w", err)
		}
		if _, err := w.Write(msg); err != nil {
			return fmt.Errorf("smtp write: %w", err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("smtp close data: %w", err)
		}
		return c.Quit()
	}

	// Port 587 or other — use standard STARTTLS via SendMail.
	auth := smtp.PlainAuth("", user, pass, host)
	return smtp.SendMail(addr, auth, from, to, msg)
}

// loginAuth implements smtp.Auth for the LOGIN mechanism used by some hosting
// providers (e.g., HostMonster/cPanel) that don't support PLAIN.
type loginAuth struct {
	username, password string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(strings.TrimSpace(string(fromServer)))
	if strings.Contains(prompt, "username") {
		return []byte(a.username), nil
	}
	if strings.Contains(prompt, "password") {
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN prompt: %q", fromServer)
}
