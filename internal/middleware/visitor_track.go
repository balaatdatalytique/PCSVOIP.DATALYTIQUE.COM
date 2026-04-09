package middleware

import (
	"net/http"
	"strings"
)

// PageViewFunc is invoked once per tracked page request. The implementation in
// internal/admin builds and stores a VisitorEvent. Decoupled here to avoid an
// import cycle (admin imports middleware for CSRF helpers).
type PageViewFunc func(ip, ua, path, referrer string)

// VisitorTrack records page_view events for top-level HTML page requests.
// Skips static assets, admin and api paths, and non-GET requests.
func VisitorTrack(track PageViewFunc, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if track == nil || !shouldTrack(r) {
			return
		}
		track(ClientIP(r), r.UserAgent(), r.URL.Path, r.Referer())
	})
}

func shouldTrack(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	if p == "" || p == "/favicon.ico" {
		return false
	}
	if strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/admin/") || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/web/") {
		return false
	}
	if p == "/" {
		return true
	}
	if strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".htm") {
		return true
	}
	return false
}
