package cryptox

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Secret wraps a base64-encoded ciphertext (nonce || ciphertext+tag) so a
// settings struct can carry encrypted fields safely. The plaintext is never
// stored on the type — call Open() with a master key already loaded to
// retrieve it. New plaintext is set with NewSecret().
type Secret struct {
	b64 string
}

// NewSecret encrypts plaintext under the loaded master key and returns a
// Secret. An empty plaintext yields an empty Secret (no encryption performed).
func NewSecret(plaintext string) (Secret, error) {
	if plaintext == "" {
		return Secret{}, nil
	}
	box, err := Encrypt([]byte(plaintext))
	if err != nil {
		return Secret{}, err
	}
	return Secret{b64: base64.StdEncoding.EncodeToString(box)}, nil
}

// SecretFromBase64 wraps a previously-stored ciphertext.
func SecretFromBase64(s string) Secret {
	return Secret{b64: strings.TrimSpace(s)}
}

// Open decrypts and returns the plaintext. Empty Secret returns "".
func (s Secret) Open() (string, error) {
	if s.b64 == "" {
		return "", nil
	}
	box, err := base64.StdEncoding.DecodeString(s.b64)
	if err != nil {
		return "", err
	}
	pt, err := Decrypt(box)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// IsEmpty reports whether the Secret has no ciphertext.
func (s Secret) IsEmpty() bool { return s.b64 == "" }

// String never reveals the ciphertext or plaintext — used for safe logging.
func (s Secret) String() string {
	if s.b64 == "" {
		return ""
	}
	return "<encrypted>"
}

// MarshalJSON serialises the ciphertext as a base64 string.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.b64)
}

// UnmarshalJSON parses a base64 string back into a Secret.
func (s *Secret) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	s.b64 = str
	return nil
}
