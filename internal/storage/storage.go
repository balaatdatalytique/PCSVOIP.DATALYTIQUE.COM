package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var allowedExtensions = map[string]bool{
	".html": true,
	".htm":  true,
	".txt":  true,
	".md":   true,
	".json": true,
	".css":  true,
	".js":   true,
}

type FileInfo struct {
	Name    string
	Path    string
	Size    int64
	ModTime time.Time
	IsDir   bool
}

type Service struct {
	baseDir   string
	backupDir string
}

func NewService(baseDir string) (*Service, error) {
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("invalid base directory: %w", err)
	}

	backupDir := filepath.Join(abs, ".cms-backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create backup directory: %w", err)
	}

	return &Service{baseDir: abs, backupDir: backupDir}, nil
}

func (s *Service) safePath(relPath string) (string, error) {
	cleaned := filepath.Clean(relPath)
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("path traversal not allowed")
	}

	full := filepath.Join(s.baseDir, cleaned)
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	if !strings.HasPrefix(abs, s.baseDir) {
		return "", fmt.Errorf("access denied: path outside content directory")
	}

	// Block access to internal directories (check both with and without trailing slash)
	restricted := []string{"/.cms-backups", "/cmd", "/internal", "/bin", "/web"}
	for _, dir := range restricted {
		restrictedPath := filepath.Join(s.baseDir, dir)
		if abs == restrictedPath || strings.HasPrefix(abs, restrictedPath+"/") {
			return "", fmt.Errorf("access denied: restricted directory")
		}
	}

	return abs, nil
}

func (s *Service) IsEditableFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return allowedExtensions[ext]
}

func (s *Service) ListFiles(relPath string) ([]FileInfo, error) {
	dir, err := s.safePath(relPath)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var files []FileInfo
	for _, e := range entries {
		name := e.Name()
		// Skip hidden files and internal dirs
		if strings.HasPrefix(name, ".") || name == "cmd" || name == "internal" || name == "bin" || name == "web" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, FileInfo{
			Name:    name,
			Path:    filepath.Join(relPath, name),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			IsDir:   e.IsDir(),
		})
	}
	return files, nil
}

func (s *Service) ReadFile(relPath string) (string, error) {
	path, err := s.safePath(relPath)
	if err != nil {
		return "", err
	}

	if !s.IsEditableFile(path) {
		return "", fmt.Errorf("file type not allowed for editing")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return string(data), nil
}

func (s *Service) WriteFile(relPath, content string) error {
	path, err := s.safePath(relPath)
	if err != nil {
		return err
	}

	if !s.IsEditableFile(path) {
		return fmt.Errorf("file type not allowed for editing")
	}

	// Create backup before writing
	if err := s.createBackup(relPath, path); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

func (s *Service) createBackup(relPath, absPath string) error {
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return nil // No existing file to back up
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}

	ts := time.Now().Format("20060102-150405")
	safeName := strings.ReplaceAll(relPath, "/", "_")
	safeName = strings.ReplaceAll(safeName, "\\", "_")
	backupFile := filepath.Join(s.backupDir, fmt.Sprintf("%s.%s.bak", safeName, ts))

	return os.WriteFile(backupFile, data, 0644)
}

func (s *Service) ListBackups(relPath string) ([]string, error) {
	safeName := strings.ReplaceAll(relPath, "/", "_")
	safeName = strings.ReplaceAll(safeName, "\\", "_")
	prefix := safeName + "."

	entries, err := os.ReadDir(s.backupDir)
	if err != nil {
		return nil, err
	}

	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			backups = append(backups, e.Name())
		}
	}
	return backups, nil
}

func (s *Service) RestoreBackup(backupName string) error {
	// Validate backup name to prevent traversal
	if strings.Contains(backupName, "/") || strings.Contains(backupName, "\\") || strings.Contains(backupName, "..") {
		return fmt.Errorf("invalid backup name")
	}

	backupPath := filepath.Join(s.backupDir, backupName)
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("backup not found: %w", err)
	}

	// Extract original relative path from backup name
	// Format: relpath.timestamp.bak
	parts := strings.Split(backupName, ".")
	if len(parts) < 3 {
		return fmt.Errorf("invalid backup format")
	}
	relPath := strings.ReplaceAll(parts[0], "_", "/")

	destPath, err := s.safePath(relPath)
	if err != nil {
		return err
	}

	return os.WriteFile(destPath, data, 0644)
}
