package config

import (
	"flag"
	"os"
	"path/filepath"
)

type Config struct {
	Port       int
	ContentDir string
	AdminUser  string
	AdminPass  string // bcrypt hash
	SessionKey string
	AIProvider string
	AIAPIKey   string
	AIModel    string
}

func Load() *Config {
	cfg := &Config{}

	flag.IntVar(&cfg.Port, "port", 8080, "Server port")
	flag.StringVar(&cfg.ContentDir, "contentDir", "", "Content directory to serve and manage")
	flag.Parse()

	if cfg.ContentDir == "" {
		wd, err := os.Getwd()
		if err == nil {
			cfg.ContentDir = wd
		} else {
			cfg.ContentDir = "."
		}
	}

	cfg.ContentDir, _ = filepath.Abs(cfg.ContentDir)

	// Admin credentials from env (password must be bcrypt hash)
	cfg.AdminUser = envOrDefault("CMS_ADMIN_USER", "admin")
	cfg.AdminPass = envOrDefault("CMS_ADMIN_PASS", "$2a$10$9jNItGGcFJxzmNeOoQQNkueQf7AbopDDkcDL96GDFHbiSDl1.PJee") // default: "admin123"
	cfg.SessionKey = envOrDefault("CMS_SESSION_KEY", "pcsvoip-cms-secret-key-change-in-production")

	// AI provider config from env (check CMS_* first, then common alternatives)
	cfg.AIProvider = envOrDefault("CMS_AI_PROVIDER", envOrDefault("AI_PROVIDER", "openai"))
	cfg.AIAPIKey = envOrDefault("CMS_AI_API_KEY", "")
	if cfg.AIAPIKey == "" {
		// Fall back to provider-specific key env vars
		switch cfg.AIProvider {
		case "grok":
			cfg.AIAPIKey = envOrDefault("GROK_API_KEY", "")
		case "openai":
			cfg.AIAPIKey = envOrDefault("OPENAI_API_KEY", "")
		case "anthropic":
			cfg.AIAPIKey = envOrDefault("ANTHROPIC_API_KEY", "")
		}
	}
	cfg.AIModel = envOrDefault("CMS_AI_MODEL", "")

	return cfg
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
