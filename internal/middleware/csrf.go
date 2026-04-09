package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

// CSRFCookieName is the cookie that holds the per-session token.
const CSRFCookieName = "csrf_token"

// CSRFFieldName is the form field expected on POST.
const CSRFFieldName = "_csrf"

// CSRF protects state-changing requests with a double-submit cookie pattern.
// On any request without a csrf_token cookie, a fresh one is set. POST/PUT/
// DELETE/PATCH requests must echo the cookie value back via _csrf form field
// or X-CSRF-Token header. Comparison is constant-time.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _ := r.Cookie(CSRFCookieName)
		if token == nil || len(token.Value) < 32 {
			fresh, _ := newCSRFToken()
			http.SetCookie(w, &http.Cookie{
				Name:     CSRFCookieName,
				Value:    fresh,
				Path:     "/",
				HttpOnly: false, // must be readable by JS for header submit
				SameSite: http.SameSiteStrictMode,
				MaxAge:   86400,
			})
			token = &http.Cookie{Value: fresh}
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		// State-changing — verify.
		// Multipart forms must be parsed before FormValue can read fields.
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		got := r.Header.Get("X-CSRF-Token")
		if got == "" {
			got = r.PostFormValue(CSRFFieldName)
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token.Value)) != 1 {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRFToken returns the current token value for embedding in templates.
func CSRFToken(r *http.Request) string {
	c, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func newCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
