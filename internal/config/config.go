package config

import (
	"bufio"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Port       int
	ContentDir string
	AdminUser  string
	AdminPass  string // bcrypt hash OR plaintext (auto-hashed at bootstrap)
	SessionKey string
	AIProvider string
	AIAPIKey   string
	AIModel    string

	// Admin module additions.
	DBPath           string // bbolt file path (default /app/data/admin.db)
	InternalAPIToken string // shared bearer for /api/* endpoints from voice-proxy
	VoiceProxyURL    string // internal address used by the bot test feature
	BotContextFile   string // legacy seed for first-run bot prompt
}

func Load() *Config {
	cfg := &Config{}

	flag.IntVar(&cfg.Port, "port", 8080, "Server port")
	flag.StringVar(&cfg.ContentDir, "contentDir", "", "Content directory to serve and manage")
	flag.Parse()

	// Load .env file (does not override existing env vars)
	loadEnvFile(".env")

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

	// Admin module
	cfg.DBPath = envOrDefault("ADMIN_DB_PATH", "/app/data/admin.db")
	cfg.InternalAPIToken = envOrDefault("INTERNAL_API_TOKEN", "")
	cfg.VoiceProxyURL = envOrDefault("VOICE_PROXY_URL", "http://pcsvoip-voice:9081")
	cfg.BotContextFile = envOrDefault("BOT_CONTEXT_FILE", filepath.Join(cfg.ContentDir, "assets", "data", "pcsvoip-context.txt"))

	return cfg
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadEnvFile reads a .env file and sets env vars that are not already set.
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // .env is optional
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Only set if not already in environment
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("WARNING: error reading .env: %v", err)
	}
}
