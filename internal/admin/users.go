package admin

import (
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"

	"pcsvoip-cms/internal/db"
)

// UserRepo handles the (single) admin user record.
type UserRepo struct{ DB *db.DB }

func NewUserRepo(database *db.DB) *UserRepo { return &UserRepo{DB: database} }

func (r *UserRepo) Get(username string) (*AdminUser, error) {
	var u AdminUser
	if err := r.DB.Get(db.BucketUsers, username, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) Create(u *AdminUser) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	return r.DB.Put(db.BucketUsers, u.Username, u)
}

func (r *UserRepo) UpdateLastLogin(username string) error {
	u, err := r.Get(username)
	if err != nil {
		return err
	}
	u.LastLogin = time.Now().UTC()
	return r.DB.Put(db.BucketUsers, u.Username, u)
}

// VerifyPassword returns true when the supplied plaintext matches the stored
// bcrypt hash.
func (r *UserRepo) VerifyPassword(username, password string) bool {
	u, err := r.Get(username)
	if err != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}

// ErrInvalidCredentials is returned by Authenticate.
var ErrInvalidCredentials = errors.New("invalid credentials")
