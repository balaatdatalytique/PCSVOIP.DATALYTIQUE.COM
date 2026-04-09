// Package routes hosts the legacy CMS file editor (now mounted under
// /admin/files/*). The new admin module under internal/admin owns its own
// templates and routing.
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
)

type Handler struct {
	CMS       *cms.Service
	Auth      *auth.Manager
	Templates *template.Template
}

// NewHandler parses the legacy CMS templates (web/templates/*.html). The new
// admin module parses its own templates from web/templates/admin/.
func NewHandler(cmsSvc *cms.Service, authMgr *auth.Manager, tmplDir string) (*Handler, error) {
	// Only parse the legacy CMS pages — not the admin subdirectory.
	files := []string{"editor.html", "ai.html", "dashboard.html"}
	t := template.New("cms")
	for _, f := range files {
		path := filepath.Join(tmplDir, f)
		if _, err := t.ParseFiles(path); err != nil {
			return nil, err
		}
	}
	return &Handler{CMS: cmsSvc, Auth: authMgr, Templates: t}, nil
}

// HandleFiles renders the file browser (replaces the old /admin/dashboard).
func (h *Handler) HandleFiles(w http.ResponseWriter, r *http.Request) {
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
	_ = h.Templates.ExecuteTemplate(w, "dashboard.html", data)
}

func (h *Handler) HandleEdit(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		http.Redirect(w, r, "/admin/files", http.StatusFound)
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
	_ = h.Templates.ExecuteTemplate(w, "editor.html", data)
}

func (h *Handler) HandlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	content := r.FormValue("content")
	filePath := r.FormValue("file")
	fileName := filepath.Base(filePath)
	if strings.HasSuffix(fileName, ".html") || strings.HasSuffix(fileName, ".htm") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(injectBaseTag(content)))
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(content))
	}
}

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
	closeIdx := strings.Index(html[idx:], ">")
	if closeIdx == -1 {
		return html
	}
	insertAt := idx + closeIdx + 1
	return html[:insertAt] + "\n<base href=\"/\">" + html[insertAt:]
}

func (h *Handler) HandleSave(w http.ResponseWriter, r *http.Request) {
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
	http.Redirect(w, r, "/admin/files/edit?file="+filePath+"&saved=1", http.StatusFound)
}

func (h *Handler) HandleAI(w http.ResponseWriter, r *http.Request) {
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
		_ = h.Templates.ExecuteTemplate(w, "ai.html", data)
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
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
	_ = json.NewEncoder(w).Encode(data)
}

func (h *Handler) HandleAIApprove(w http.ResponseWriter, r *http.Request) {
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
	http.Redirect(w, r, "/admin/files/edit?file="+filePath+"&saved=1", http.StatusFound)
}
