package admin

import (
	"errors"
	"time"

	"pcsvoip-cms/internal/cryptox"
	"pcsvoip-cms/internal/db"
)

const settingsKey = "global"

// SettingsRepo wraps the bbolt-backed Settings sheet.
type SettingsRepo struct{ DB *db.DB }

func NewSettingsRepo(database *db.DB) *SettingsRepo { return &SettingsRepo{DB: database} }

// Get returns the current settings, creating an empty record on first call.
func (r *SettingsRepo) Get() (*Settings, error) {
	var s Settings
	err := r.DB.Get(db.BucketSettings, settingsKey, &s)
	if errors.Is(err, db.ErrNotFound) {
		s = Settings{
			AIProvider: "grok",
			SMTPPort:   587,
			SMTPUseTLS: true,
			UpdatedAt:  time.Now().UTC(),
		}
		if perr := r.DB.Put(db.BucketSettings, settingsKey, s); perr != nil {
			return nil, perr
		}
		return &s, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Save persists the settings. Caller is responsible for converting plaintext
// secret inputs into cryptox.Secret values via SetSecretIfChanged.
func (r *SettingsRepo) Save(s *Settings) error {
	s.UpdatedAt = time.Now().UTC()
	return r.DB.Put(db.BucketSettings, settingsKey, s)
}

// SetSecretIfChanged keeps the existing ciphertext when input is empty (a blank
// form field means "leave unchanged"), and re-encrypts otherwise.
func SetSecretIfChanged(field *cryptox.Secret, input string) error {
	if input == "" {
		return nil
	}
	enc, err := cryptox.NewSecret(input)
	if err != nil {
		return err
	}
	*field = enc
	return nil
}
