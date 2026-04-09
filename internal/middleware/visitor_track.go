package middleware

import (
	"net"
	"net/http"
	"strings"
)

// PageViewFunc is invoked once per tracked page request. The implementation in
// internal/admin builds and stores a VisitorEvent. Decoupled here to avoid an
// import cycle (admin imports middleware for CSRF helpers).
type PageViewFunc func(ip, ua, path, referrer string)

// VisitorTrack records page_view events for top-level HTML page requests.
// Skips static assets, admin and api paths, non-GET requests, bot user-agents
// and any traffic where the resolved client IP is private or loopback (Docker
// healthcheck noise, internal probes).
func VisitorTrack(track PageViewFunc, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if track == nil || !shouldTrack(r) {
			return
		}
		ip := ClientIP(r)
		if !IsPublicIP(ip) {
			return
		}
		track(ip, r.UserAgent(), r.URL.Path, r.Referer())
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
	if !(p == "/" || strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".htm")) {
		return false
	}
	if IsBotUA(r.UserAgent()) {
		return false
	}
	return true
}

// IsBotUA returns true for known crawlers, monitors, healthcheck probes and
// command-line http clients. Conservative — better to drop a real visitor than
// fill the dashboard with noise.
func IsBotUA(ua string) bool {
	if ua == "" {
		return true
	}
	u := strings.ToLower(ua)
	for _, needle := range botUAFragments {
		if strings.Contains(u, needle) {
			return true
		}
	}
	return false
}

var botUAFragments = []string{
	"wget", "curl", "kube-probe", "googlehc", "healthcheck", "go-http-client",
	"httpclient", "python-requests", "phantomjs", "headlesschrome",
	"googlebot", "bingbot", "baiduspider", "yandexbot", "applebot",
	"ahrefsbot", "semrushbot", "mj12bot", "dotbot", "petalbot",
	"facebookexternalhit", "twitterbot", "linkedinbot", "duckduckbot",
	"slackbot", "discordbot", "telegrambot", "whatsapp",
	"uptimerobot", "monitor", "pingdom", "newrelic", "datadog",
}

// IsPublicIP returns true when ip is a parseable, non-loopback, non-private,
// non-link-local address. Used to drop healthcheck and proxy-internal traffic
// before it gets recorded as a visitor.
func IsPublicIP(ip string) bool {
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
