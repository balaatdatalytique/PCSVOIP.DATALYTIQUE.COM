package routes

import (
	"encoding/json"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"pcsvoip-cms/internal/auth"
	"pcsvoip-cms/internal/cms"
	"pcsvoip-cms/internal/middleware"
)

type Handler struct {
	CMS       *cms.Service
	Auth      *auth.Manager
	Templates *template.Template
}

func NewHandler(cmsSvc *cms.Service, authMgr *auth.Manager, tmplDir string) (*Handler, error) {
	tmpl, err := template.ParseGlob(filepath.Join(tmplDir, "*.html"))
	if err != nil {
		return nil, err
	}
	return &Handler{
		CMS:       cmsSvc,
		Auth:      authMgr,
		Templates: tmpl,
	}, nil
}

func (h *Handler) SetupRoutes(mux *http.ServeMux, contentDir string) {
	// Admin routes (protected)
	protected := http.NewServeMux()
	protected.HandleFunc("/admin/dashboard", h.handleDashboard)
	protected.HandleFunc("/admin/edit", h.handleEdit)
	protected.HandleFunc("/admin/preview", h.handlePreview)
	protected.HandleFunc("/admin/save", h.handleSave)
	protected.HandleFunc("/admin/ai", h.handleAI)
	protected.HandleFunc("/admin/ai/approve", h.handleAIApprove)
	protected.HandleFunc("/admin/logout", h.handleLogout)

	// Public admin routes
	mux.HandleFunc("/admin/login", h.handleLogin)
	mux.Handle("/admin/", middleware.Auth(h.Auth, protected))

	// Static file serving (must be last)
	fs := http.FileServer(http.Dir(contentDir))
	mux.Handle("/", fs)
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.Templates.ExecuteTemplate(w, "login.html", nil)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	if !h.Auth.Authenticate(username, password) {
		h.Templates.ExecuteTemplate(w, "login.html", map[string]string{"Error": "Invalid credentials"})
		return
	}

	token, err := h.Auth.CreateSession(username)
	if err != nil {
		http.Error(w, "Session error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "cms_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400,
	})

	http.Redirect(w, r, "/admin/dashboard", http.StatusFound)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := h.Auth.GetSessionToken(r)
	if token != "" {
		h.Auth.DestroySession(token)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "cms_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = "/"
	}

	files, err := h.CMS.ListFiles(dir)
	if err != nil {
		http.Error(w, "Error listing files: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"Files":      files,
		"CurrentDir": dir,
		"ParentDir":  filepath.Dir(strings.TrimSuffix(dir, "/")),
	}
	h.Templates.ExecuteTemplate(w, "dashboard.html", data)
}

func (h *Handler) handleEdit(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		http.Redirect(w, r, "/admin/dashboard", http.StatusFound)
		return
	}

	content, err := h.CMS.LoadContent(filePath)
	if err != nil {
		http.Error(w, "Error loading file: "+err.Error(), http.StatusForbidden)
		return
	}

	data := map[string]interface{}{
		"FilePath": filePath,
		"Content":  content,
		"FileName": filepath.Base(filePath),
	}
	h.Templates.ExecuteTemplate(w, "editor.html", data)
}

func (h *Handler) handlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	content := r.FormValue("content")
	filePath := r.FormValue("file")
	fileName := filepath.Base(filePath)

	if strings.HasSuffix(fileName, ".html") || strings.HasSuffix(fileName, ".htm") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Inject <base href="/"> so relative asset paths (css, js, images) resolve
		// correctly when the preview is served from /admin/preview.
		html := injectBaseTag(content)
		w.Write([]byte(html))
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(content))
	}
}

// injectBaseTag adds <base href="/"> after <head> so relative paths resolve from root.
// Skips injection if a <base> tag already exists.
func injectBaseTag(html string) string {
	lower := strings.ToLower(html)
	if strings.Contains(lower, "<base ") {
		return html
	}
	idx := strings.Index(lower, "<head>")
	if idx == -1 {
		idx = strings.Index(lower, "<head ")
	}
	if idx == -1 {
		return html
	}
	// Find the closing > of the <head...> tag
	closeIdx := strings.Index(html[idx:], ">")
	if closeIdx == -1 {
		return html
	}
	insertAt := idx + closeIdx + 1
	return html[:insertAt] + "\n<base href=\"/\">" + html[insertAt:]
}

func (h *Handler) handleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filePath := r.FormValue("file")
	content := r.FormValue("content")

	if filePath == "" || content == "" {
		http.Error(w, "Missing file path or content", http.StatusBadRequest)
		return
	}

	if err := h.CMS.SaveContent(filePath, content); err != nil {
		http.Error(w, "Error saving: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/edit?file="+filePath+"&saved=1", http.StatusFound)
}

func (h *Handler) handleAI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		filePath := r.URL.Query().Get("file")
		content := ""
		if filePath != "" {
			content, _ = h.CMS.LoadContent(filePath)
		}
		data := map[string]interface{}{
			"FilePath": filePath,
			"Content":  content,
			"FileName": filepath.Base(filePath),
		}
		h.Templates.ExecuteTemplate(w, "ai.html", data)
		return
	}

	filePath := r.FormValue("file")
	instruction := r.FormValue("instruction")

	if filePath == "" || instruction == "" {
		http.Error(w, "Missing file or instruction", http.StatusBadRequest)
		return
	}

	result, err := h.CMS.AIEdit(filePath, instruction)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"FilePath": filePath,
			"Error":    err.Error(),
			"FileName": filepath.Base(filePath),
		})
		return
	}

	data := map[string]interface{}{
		"FilePath":    filePath,
		"FileName":    filepath.Base(filePath),
		"Instruction": instruction,
		"Result":      result,
		"Timestamp":   time.Now().Format("2006-01-02 15:04:05"),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) handleAIApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filePath := r.FormValue("file")
	content := r.FormValue("content")

	if filePath == "" || content == "" {
		http.Error(w, "Missing file or content", http.StatusBadRequest)
		return
	}

	if err := h.CMS.SaveContent(filePath, content); err != nil {
		http.Error(w, "Error saving AI content: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/edit?file="+filePath+"&saved=1", http.StatusFound)
}
