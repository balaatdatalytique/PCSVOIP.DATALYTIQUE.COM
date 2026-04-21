package admin

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pcsvoip-cms/internal/cryptox"
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
	s, err := settings.Get()
	if err != nil {
		return err
	}
	// Sync SMTP settings from env vars when SMTP_HOST is set.
	if host := os.Getenv("SMTP_HOST"); host != "" {
		s.SMTPHost = host
		if port, _ := strconv.Atoi(os.Getenv("SMTP_PORT")); port > 0 {
			s.SMTPPort = port
		}
		s.SMTPUser = os.Getenv("SMTP_USER")
		if pw := os.Getenv("SMTP_PASS"); pw != "" {
			enc, err := cryptox.NewSecret(pw)
			if err != nil {
				return err
			}
			s.SMTPPass = enc
		}
		s.SMTPFromEmail = os.Getenv("SMTP_FROM_EMAIL")
		s.SMTPFromName = os.Getenv("SMTP_FROM_NAME")
		s.SMTPUseTLS = os.Getenv("SMTP_USE_TLS") != "false"
		if ae := os.Getenv("SMTP_ADMIN_EMAIL"); ae != "" {
			s.AdminEmail = ae
		}
		if err := settings.Save(s); err != nil {
			return err
		}
		log.Printf("admin: synced SMTP settings from environment")
	}

	// Sweep any pre-filter healthcheck / bot noise so the dashboard starts
	// clean. New events are filtered at write time by the visitor middleware.
	if err := cleanupNoiseEvents(database); err != nil {
		log.Printf("admin: cleanup noise: %v", err)
	}
	return nil
}

// cleanupNoiseEvents deletes events whose IP is loopback/private/empty and
// visitor summaries with the same. Runs once per process start; the cost is
// O(events) so it's bounded by whatever made it through previously.
func cleanupNoiseEvents(database *db.DB) error {
	var (
		evDel  []string
		visDel []string
	)
	err := database.ForEach(db.BucketEvents, func(key string, raw []byte) error {
		var ev VisitorEvent
		if e := json.Unmarshal(raw, &ev); e != nil {
			return nil
		}
		if !isPublicIPBoot(ev.IP) {
			evDel = append(evDel, key)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, k := range evDel {
		_ = database.Delete(db.BucketEvents, k)
	}

	err = database.ForEach(db.BucketVisitors, func(key string, raw []byte) error {
		var v Visitor
		if e := json.Unmarshal(raw, &v); e != nil {
			return nil
		}
		if !isPublicIPBoot(v.LastIP) {
			visDel = append(visDel, key)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, k := range visDel {
		_ = database.Delete(db.BucketVisitors, k)
	}

	if len(evDel) > 0 || len(visDel) > 0 {
		log.Printf("admin: cleanup removed %d noise events and %d visitor summaries", len(evDel), len(visDel))
	}
	return nil
}

func isPublicIPBoot(ip string) bool {
	if ip == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsLinkLocalUnicast() || parsed.IsUnspecified() {
		return false
	}
	return true
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
