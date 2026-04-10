package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"pcsvoip-cms/internal/auth"
	"pcsvoip-cms/internal/cryptox"
	"pcsvoip-cms/internal/middleware"
)

// Handler bundles every dependency the admin module needs at request time.
type Handler struct {
	Auth          *auth.Manager
	Users         *UserRepo
	Settings      *SettingsRepo
	Bot           *BotRepo
	KB            *KBRepo
	Visitors      *VisitorRepo
	Pages         map[string]*template.Template // one template.Template per page (layout + page)
	Login         *template.Template
	StaticDir     string
	UploadDir     string
	VoiceProxyURL string // e.g. http://pcsvoip-voice:9081
}

// New parses templates and returns a wired Handler. Each admin page is parsed
// alongside _layout.html so that they don't clash on the shared "page" block.
func New(deps Handler, templateDir string) (*Handler, error) {
	h := deps
	pages := []string{"dashboard.html", "bot.html", "kb_list.html", "kb_form.html", "visitors.html", "visitor_view.html", "settings.html"}
	layoutPath := filepath.Join(templateDir, "_layout.html")
	h.Pages = make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		t := template.New(p).Funcs(adminFuncs)
		if _, err := t.ParseFiles(layoutPath, filepath.Join(templateDir, p)); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		h.Pages[p] = t
	}
	loginTmpl, err := template.New("login").Funcs(adminFuncs).ParseFiles(filepath.Join(templateDir, "login.html"))
	if err != nil {
		return nil, fmt.Errorf("parse login: %w", err)
	}
	h.Login = loginTmpl
	return &h, nil
}

var adminFuncs = template.FuncMap{
	"fmtTime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Format("2006-01-02 15:04")
	},
	"fmtBytes": func(n int) string {
		if n < 1024 {
			return fmt.Sprintf("%d B", n)
		}
		if n < 1024*1024 {
			return fmt.Sprintf("%.1f KB", float64(n)/1024)
		}
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	},
	"join": func(sep string, parts []string) string { return strings.Join(parts, sep) },
	"truncate": func(n int, s string) string {
		if len(s) <= n {
			return s
		}
		return s[:n] + "…"
	},
	"eventLabel": FormatType,
	"flag":       FlagEmoji,
	"add":        func(a, b int) int { return a + b },
	"sub":        func(a, b int) int { return a - b },
	"fmtDuration": func(secs int) string {
		if secs <= 0 {
			return "—"
		}
		if secs < 60 {
			return fmt.Sprintf("%ds", secs)
		}
		if secs < 3600 {
			return fmt.Sprintf("%dm %ds", secs/60, secs%60)
		}
		return fmt.Sprintf("%dh %dm", secs/3600, (secs%3600)/60)
	},
	"summaryFor": func(stats map[string]VisitorPageSummary, id string) VisitorPageSummary {
		return stats[id]
	},
	"seq": func(start, end int) []int {
		if end < start {
			return nil
		}
		out := make([]int, 0, end-start+1)
		for i := start; i <= end; i++ {
			out = append(out, i)
		}
		return out
	},
}

// =====================================================================
// LOGIN / LOGOUT
// =====================================================================

// LoginPage renders the login form (and handles POST).
func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.renderLogin(w, r, "")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	if !h.Users.VerifyPassword(username, password) {
		h.renderLogin(w, r, "Invalid username or password")
		return
	}
	if err := h.Users.UpdateLastLogin(username); err != nil {
		log.Printf("admin: update last_login: %v", err)
	}
	token, err := h.Auth.CreateSession(username)
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "cms_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   isHTTPS(r),
		MaxAge:   int(h.Auth.SessionTTL().Seconds()),
	})
	http.Redirect(w, r, "/admin/dashboard", http.StatusFound)
}

// Logout clears the session and bounces to login.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	token := h.Auth.GetSessionToken(r)
	if token != "" {
		h.Auth.DestroySession(token)
	}
	http.SetCookie(w, &http.Cookie{
		Name: "cms_session", Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, errorMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.Login.ExecuteTemplate(w, "login.html", map[string]any{
		"Error":     errorMsg,
		"CSRFToken": middleware.CSRFToken(r),
	})
}

// =====================================================================
// DASHBOARD (overview)
// =====================================================================

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	totalVisitors, _ := h.Visitors.CountVisitors()
	since24h := time.Now().Add(-24 * time.Hour)
	events24h, _ := h.Visitors.CountEventsSince(since24h)
	kbCount, _ := h.KB.Count()
	kbActive, _ := h.KB.CountActive()
	botCfg, _ := h.Bot.Get()
	recent, _ := h.Visitors.RecentEvents(15)

	h.render(w, r, "dashboard.html", map[string]any{
		"Title":         "Overview",
		"NavActive":     "dashboard",
		"TotalVisitors": totalVisitors,
		"Events24h":     events24h,
		"KBCount":       kbCount,
		"KBActive":      kbActive,
		"BotEnabled":    botCfg != nil && botCfg.Enabled,
		"BotUpdatedAt":  botCfg.UpdatedAt,
		"Recent":        recent,
	})
}

// =====================================================================
// BOT CONTROL
// =====================================================================

func (h *Handler) BotPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		h.botSave(w, r)
		return
	}
	cfg, err := h.Bot.Get()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "bot.html", map[string]any{
		"Title":     "Bot Control",
		"NavActive": "bot",
		"Bot":       cfg,
		"Saved":     r.URL.Query().Get("saved") == "1",
	})
}

func (h *Handler) botSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	cfg, err := h.Bot.Get()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg.Enabled = r.PostFormValue("enabled") == "on"
	cfg.Persona = strings.TrimSpace(r.PostFormValue("persona"))
	cfg.Tone = strings.TrimSpace(r.PostFormValue("tone"))
	cfg.SystemPrompt = strings.TrimSpace(r.PostFormValue("system_prompt"))
	cfg.Greeting = strings.TrimSpace(r.PostFormValue("greeting"))
	cfg.Guardrails = strings.TrimSpace(r.PostFormValue("guardrails"))
	cfg.Voice = strings.TrimSpace(r.PostFormValue("voice"))
	if cfg.Voice == "" {
		cfg.Voice = "ara"
	}
	if v, err := strconv.Atoi(r.PostFormValue("max_kb_bytes")); err == nil && v > 0 {
		cfg.MaxKBBytes = v
	}
	if err := h.Bot.Save(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/bot?saved=1", http.StatusFound)
}

// BotTest sends a test message through voice-proxy and returns the reply.
func (h *Handler) BotTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, `{"error":"bad form"}`, http.StatusBadRequest)
		return
	}
	msg := strings.TrimSpace(r.PostFormValue("message"))
	if msg == "" {
		http.Error(w, `{"error":"empty message"}`, http.StatusBadRequest)
		return
	}
	if h.VoiceProxyURL == "" {
		json.NewEncoder(w).Encode(map[string]string{"error": "voice-proxy URL not configured"})
		return
	}
	body, _ := json.Marshal(map[string]string{
		"session_id": "admin-test-" + NewID(),
		"message":    msg,
		"first_name": "Admin",
		"last_name":  "Test",
	})
	req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(h.VoiceProxyURL, "/")+"/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("voice-proxy returned %d: %s", resp.StatusCode, string(respBody))})
		return
	}
	w.Write(respBody)
}

// =====================================================================
// KNOWLEDGE BASE
// =====================================================================

func (h *Handler) KBList(w http.ResponseWriter, r *http.Request) {
	docs, err := h.KB.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q != "" {
		filtered := make([]KBDocument, 0, len(docs))
		for _, d := range docs {
			if strings.Contains(strings.ToLower(d.Title), q) ||
				strings.Contains(strings.ToLower(strings.Join(d.Tags, " ")), q) {
				filtered = append(filtered, d)
			}
		}
		docs = filtered
	}
	h.render(w, r, "kb_list.html", map[string]any{
		"Title":     "Knowledge Base",
		"NavActive": "kb",
		"Docs":      docs,
		"Query":     q,
	})
}

func (h *Handler) KBNew(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "kb_form.html", map[string]any{
		"Title":     "Add Knowledge",
		"NavActive": "kb",
		"Doc":       &KBDocument{Active: true},
		"IsNew":     true,
	})
}

func (h *Handler) KBCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		// Fall back to plain form parse if not multipart.
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
	}
	doc := &KBDocument{
		Title:  strings.TrimSpace(r.PostFormValue("title")),
		Tags:   ParseTags(r.PostFormValue("tags")),
		Active: r.PostFormValue("active") == "on",
		Source: "paste",
	}
	// File takes precedence if present.
	if r.MultipartForm != nil {
		if files := r.MultipartForm.File["file"]; len(files) > 0 {
			fh := files[0]
			if !IsAllowedDoc(fh.Filename) {
				http.Error(w, "unsupported file type", http.StatusBadRequest)
				return
			}
			tmp, err := os.CreateTemp("", "kbupload-*"+filepath.Ext(fh.Filename))
			if err != nil {
				http.Error(w, "tmp file: "+err.Error(), http.StatusInternalServerError)
				return
			}
			defer os.Remove(tmp.Name())
			src, err := fh.Open()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer src.Close()
			if _, err := io.Copy(tmp, src); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			tmp.Close()
			text, err := ExtractText(tmp.Name())
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			doc.Content = text
			doc.Source = "upload"
			doc.SourceName = fh.Filename
			if doc.Title == "" {
				doc.Title = strings.TrimSuffix(fh.Filename, filepath.Ext(fh.Filename))
			}
		}
	}
	if doc.Content == "" {
		doc.Content = strings.TrimSpace(r.PostFormValue("content"))
	}
	if doc.Title == "" {
		doc.Title = "Untitled"
	}
	if strings.TrimSpace(doc.Content) == "" {
		http.Error(w, "no content provided", http.StatusBadRequest)
		return
	}
	if err := h.KB.Create(doc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/kb?toast=Document+created", http.StatusFound)
}

func (h *Handler) KBEdit(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	doc, err := h.KB.Get(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	h.render(w, r, "kb_form.html", map[string]any{
		"Title":     "Edit: " + doc.Title,
		"NavActive": "kb",
		"Doc":       doc,
		"IsNew":     false,
	})
}

func (h *Handler) KBUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id := r.PostFormValue("id")
	doc, err := h.KB.Get(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	doc.Title = strings.TrimSpace(r.PostFormValue("title"))
	doc.Tags = ParseTags(r.PostFormValue("tags"))
	doc.Content = strings.TrimSpace(r.PostFormValue("content"))
	doc.Active = r.PostFormValue("active") == "on"
	if err := h.KB.Update(doc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/kb?toast=Document+updated", http.StatusFound)
}

func (h *Handler) KBDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id := r.PostFormValue("id")
	if err := h.KB.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/kb?toast=Document+deleted", http.StatusFound)
}

func (h *Handler) KBToggle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id := r.PostFormValue("id")
	if err := h.KB.ToggleActive(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/kb?toast=Document+toggled", http.StatusFound)
}

// =====================================================================
// VISITORS
// =====================================================================

func (h *Handler) VisitorsPage(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r.URL.Query().Get("page"))
	vpage := parsePage(r.URL.Query().Get("vpage"))
	epp := parsePerPage(r.URL.Query().Get("epp"))
	vpp := parsePerPage(r.URL.Query().Get("vpp"))

	eventsPage, err := h.Visitors.RecentEventsPaged(page, epp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	visitorsPage, err := h.Visitors.ListVisitorsPaged(vpage, vpp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Enrich the visible visitors with their top page + total dwell in one
	// pass over the events bucket.
	ids := make([]string, 0, len(visitorsPage.Visitors))
	for _, v := range visitorsPage.Visitors {
		ids = append(ids, v.ID)
	}
	pageSummaries, _ := h.Visitors.PageStatsForVisitors(ids)

	// 24-hour rolling counts by type (independent of pagination).
	since := time.Now().Add(-24 * time.Hour)
	pageViews24h, chats24h, voices24h := h.Visitors.CountByTypeSince(since)

	h.render(w, r, "visitors.html", map[string]any{
		"Title":     "Visitors",
		"NavActive": "visitors",
		"Events":    eventsPage,
		"Visitors":  visitorsPage,
		"PageStats": pageSummaries,
		"Counts": map[string]int{
			"page_view": pageViews24h,
			"bot_chat":  chats24h,
			"bot_voice": voices24h,
		},
	})
}

// VisitorView renders the per-visitor drill-down page (page history, top
// pages, dwell times, bot interactions).
func (h *Handler) VisitorView(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Redirect(w, r, "/admin/visitors", http.StatusFound)
		return
	}
	detail, err := h.Visitors.GetVisitorDetail(id)
	if err != nil {
		http.Error(w, "visitor not found", http.StatusNotFound)
		return
	}
	h.render(w, r, "visitor_view.html", map[string]any{
		"Title":     "Visitor " + id[:12],
		"NavActive": "visitors",
		"Detail":    detail,
	})
}

func parsePage(s string) int {
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func parsePerPage(s string) int {
	if s == "" {
		return 50
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 50
	}
	// Allow only specific sizes.
	switch n {
	case 10, 25, 50, 100, 200:
		return n
	default:
		return 50
	}
}

// =====================================================================
// SETTINGS
// =====================================================================

func (h *Handler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		h.settingsSave(w, r)
		return
	}
	s, err := h.Settings.Get()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "settings.html", map[string]any{
		"Title":      "Settings",
		"NavActive":  "settings",
		"Settings":   s,
		"Saved":      r.URL.Query().Get("saved") == "1",
		"Tab":        firstNonEmpty(r.URL.Query().Get("tab"), "ai"),
	})
}

func (h *Handler) settingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	s, err := h.Settings.Get()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.AIProvider = strings.TrimSpace(r.PostFormValue("ai_provider"))
	s.AIModel = strings.TrimSpace(r.PostFormValue("ai_model"))
	if err := SetSecretIfChanged(&s.GrokAPIKey, r.PostFormValue("grok_api_key")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := SetSecretIfChanged(&s.OpenAIAPIKey, r.PostFormValue("openai_api_key")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := SetSecretIfChanged(&s.AnthropicAPIKey, r.PostFormValue("anthropic_api_key")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.SMTPHost = strings.TrimSpace(r.PostFormValue("smtp_host"))
	if v, err := strconv.Atoi(r.PostFormValue("smtp_port")); err == nil {
		s.SMTPPort = v
	}
	s.SMTPUser = strings.TrimSpace(r.PostFormValue("smtp_user"))
	if err := SetSecretIfChanged(&s.SMTPPass, r.PostFormValue("smtp_pass")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.SMTPFromEmail = strings.TrimSpace(r.PostFormValue("smtp_from_email"))
	s.SMTPFromName = strings.TrimSpace(r.PostFormValue("smtp_from_name"))
	s.SMTPUseTLS = r.PostFormValue("smtp_use_tls") == "on"
	s.AdminEmail = strings.TrimSpace(r.PostFormValue("admin_email"))
	s.SiteURL = strings.TrimSpace(r.PostFormValue("site_url"))
	if err := h.Settings.Save(s); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/settings?saved=1&tab="+r.PostFormValue("tab"), http.StatusFound)
}

// SettingsTestSMTP sends a test email to AdminEmail using the saved SMTP creds.
func (h *Handler) SettingsTestSMTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	s, err := h.Settings.Get()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if s.SMTPHost == "" || s.SMTPPort == 0 || s.AdminEmail == "" {
		json.NewEncoder(w).Encode(map[string]string{"error": "Configure SMTP host, port and admin email first."})
		return
	}
	pass, err := s.SMTPPass.Open()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": "decrypt smtp pass: " + err.Error()})
		return
	}
	auth := smtp.PlainAuth("", s.SMTPUser, pass, s.SMTPHost)
	addr := fmt.Sprintf("%s:%d", s.SMTPHost, s.SMTPPort)
	from := s.SMTPFromEmail
	if from == "" {
		from = s.SMTPUser
	}
	msg := []byte("To: " + s.AdminEmail + "\r\n" +
		"From: " + from + "\r\n" +
		"Subject: PCS VoIP admin SMTP test\r\n" +
		"\r\nThis is a test email from your PCS VoIP admin module. SMTP is working.\r\n")
	if err := smtp.SendMail(addr, auth, from, []string{s.AdminEmail}, msg); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"ok": "Test email sent to " + s.AdminEmail})
}

// =====================================================================
// HELPERS
// =====================================================================

// render executes the named template inside the shared _layout.html.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["CSRFToken"] = middleware.CSRFToken(r)
	data["Page"] = strings.TrimSuffix(page, ".html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, ok := h.Pages[page]
	if !ok {
		http.Error(w, "unknown template: "+page, http.StatusInternalServerError)
		return
	}
	// Each page parses _layout.html alongside itself; render the layout entry.
	if err := t.ExecuteTemplate(w, "_layout", data); err != nil {
		log.Printf("admin: template %s: %v", page, err)
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// SecretSet reports whether a Secret has a value (used by templates).
func SecretSet(s cryptox.Secret) bool { return !s.IsEmpty() }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

// SortedTags returns a sorted unique tag list across the KB (used by future filter).
func SortedTags(docs []KBDocument) []string {
	set := map[string]bool{}
	for _, d := range docs {
		for _, t := range d.Tags {
			set[t] = true
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
