// Package admin holds the data models, repositories and HTTP handlers for the
// PCS VoIP administration module.
package admin

import (
	"time"

	"pcsvoip-cms/internal/cryptox"
)

// AdminUser is the (single) administrator. Bootstrapped from env on first run.
type AdminUser struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"` // bcrypt
	CreatedAt    time.Time `json:"created_at"`
	LastLogin    time.Time `json:"last_login,omitempty"`
}

// Settings is the global key-value sheet shown on the Settings page. Sensitive
// fields are persisted as cryptox.Secret (AES-GCM at rest).
type Settings struct {
	AIProvider      string         `json:"ai_provider"` // "grok" | "openai" | "anthropic"
	AIModel         string         `json:"ai_model"`
	GrokAPIKey      cryptox.Secret `json:"grok_api_key"`
	OpenAIAPIKey    cryptox.Secret `json:"openai_api_key"`
	AnthropicAPIKey cryptox.Secret `json:"anthropic_api_key"`

	SMTPHost      string         `json:"smtp_host"`
	SMTPPort      int            `json:"smtp_port"`
	SMTPUser      string         `json:"smtp_user"`
	SMTPPass      cryptox.Secret `json:"smtp_pass"`
	SMTPFromEmail string         `json:"smtp_from_email"`
	SMTPFromName  string         `json:"smtp_from_name"`
	SMTPUseTLS    bool           `json:"smtp_use_tls"`

	AdminEmail string    `json:"admin_email"`
	SiteURL    string    `json:"site_url"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// BotConfig drives both the text chatbot and the voice bot.
type BotConfig struct {
	Enabled      bool      `json:"enabled"`
	Persona      string    `json:"persona"`
	Tone         string    `json:"tone"`
	SystemPrompt string    `json:"system_prompt"`
	Greeting     string    `json:"greeting"`
	Guardrails   string    `json:"guardrails"`
	MaxKBBytes   int       `json:"max_kb_bytes"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// KBDocument is one entry in the knowledge base. Active=true documents are
// concatenated into the bot system prompt at request time.
type KBDocument struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Source     string    `json:"source"` // "upload" | "paste"
	SourceName string    `json:"source_name,omitempty"`
	Tags       []string  `json:"tags,omitempty"`
	Content    string    `json:"content"`
	Bytes      int       `json:"bytes"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Visitor is the rolled-up summary keyed by sha1(ip+ua)[:16].
type Visitor struct {
	ID          string    `json:"id"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	PageViews   int       `json:"page_views"`
	BotMsgs     int       `json:"bot_msgs"`
	UserAgent   string    `json:"user_agent"`
	LastIP      string    `json:"last_ip"`
	City        string    `json:"city,omitempty"`
	Region      string    `json:"region,omitempty"`
	Country     string    `json:"country,omitempty"`
	CountryCode string    `json:"country_code,omitempty"`
	ISP         string    `json:"isp,omitempty"`
	Latitude    float64   `json:"latitude,omitempty"`
	Longitude   float64   `json:"longitude,omitempty"`
}

// GeoInfo is the cached IP geolocation result. Stored under BucketGeoCache
// keyed by IP. Refreshed after geoTTL.
type GeoInfo struct {
	IP          string    `json:"ip"`
	City        string    `json:"city,omitempty"`
	Region      string    `json:"region,omitempty"`
	Country     string    `json:"country,omitempty"`
	CountryCode string    `json:"country_code,omitempty"`
	ISP         string    `json:"isp,omitempty"`
	Latitude    float64   `json:"lat,omitempty"`
	Longitude   float64   `json:"lon,omitempty"`
	Timezone    string    `json:"timezone,omitempty"`
	FetchedAt   time.Time `json:"fetched_at"`
}

// VisitorEvent is one row in the chronological event log.
type VisitorEvent struct {
	ID        string    `json:"id"` // ulid (lex-orderable by time)
	VisitorID string    `json:"visitor_id"`
	Type      string    `json:"type"` // "page_view" | "bot_chat" | "bot_voice"
	Path      string    `json:"path,omitempty"`
	Referrer  string    `json:"referrer,omitempty"`
	Message   string    `json:"message,omitempty"`
	Reply     string    `json:"reply,omitempty"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
