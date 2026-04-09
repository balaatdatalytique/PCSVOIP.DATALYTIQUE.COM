package admin

import (
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pcsvoip-cms/internal/db"
)

// Bootstrap ensures the admin user, default bot config, and default settings
// exist. Called at server start.
func Bootstrap(database *db.DB, adminUser, adminPassEnv, contextFilePath string) error {
	if adminUser == "" {
		adminUser = "admin"
	}
	users := NewUserRepo(database)
	if _, err := users.Get(adminUser); errors.Is(err, db.ErrNotFound) {
		hash, err := normaliseHash(adminPassEnv)
		if err != nil {
			return err
		}
		if err := users.Create(&AdminUser{
			Username:     adminUser,
			PasswordHash: hash,
			CreatedAt:    time.Now().UTC(),
		}); err != nil {
			return err
		}
		log.Printf("admin: bootstrapped admin user %q", adminUser)
	} else if err != nil {
		return err
	}

	bot := NewBotRepo(database)
	if _, err := bot.DB.GetRaw(db.BucketBot, botKey); errors.Is(err, db.ErrNotFound) {
		cfg := defaultBotConfig()
		// Seed system prompt from existing context file if present.
		if contextFilePath != "" {
			if data, err := os.ReadFile(contextFilePath); err == nil && len(data) > 0 {
				cfg.SystemPrompt = strings.TrimSpace(string(data))
				log.Printf("admin: seeded bot system prompt from %s (%d bytes)", contextFilePath, len(data))
			}
		}
		if err := bot.Save(&cfg); err != nil {
			return err
		}
	}

	settings := NewSettingsRepo(database)
	if _, err := settings.Get(); err != nil {
		return err
	}
	return nil
}

// normaliseHash accepts either a bcrypt hash or a plaintext password and
// returns a bcrypt hash. We detect bcrypt by the standard prefix.
func normaliseHash(in string) (string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		// emergency default — log loudly
		log.Println("admin: WARNING — no admin password supplied; defaulting to 'admin123' (CHANGE THIS)")
		in = "admin123"
	}
	if strings.HasPrefix(in, "$2a$") || strings.HasPrefix(in, "$2b$") || strings.HasPrefix(in, "$2y$") {
		return in, nil
	}
	h, err := bcrypt.GenerateFromPassword([]byte(in), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}
