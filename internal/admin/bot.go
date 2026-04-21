package admin

import (
	"errors"
	"strings"
	"time"

	"pcsvoip-cms/internal/db"
)

const botKey = "global"

// BotRepo wraps the bbolt-backed BotConfig.
type BotRepo struct{ DB *db.DB }

func NewBotRepo(database *db.DB) *BotRepo { return &BotRepo{DB: database} }

// Get returns the current bot config, defaulting on first call. Also fills in
// any zero-value fields that were added after the original record was written
// (forward-compat for schema additions like Voice).
func (r *BotRepo) Get() (*BotConfig, error) {
	var b BotConfig
	err := r.DB.Get(db.BucketBot, botKey, &b)
	if errors.Is(err, db.ErrNotFound) {
		b = defaultBotConfig()
		if perr := r.DB.Put(db.BucketBot, botKey, b); perr != nil {
			return nil, perr
		}
		return &b, nil
	}
	if err != nil {
		return nil, err
	}
	if b.Voice == "" {
		b.Voice = "ara"
	}
	return &b, nil
}

// Save persists the bot config.
func (r *BotRepo) Save(b *BotConfig) error {
	b.UpdatedAt = time.Now().UTC()
	if b.MaxKBBytes <= 0 {
		b.MaxKBBytes = 30000
	}
	return r.DB.Put(db.BucketBot, botKey, b)
}

func defaultBotConfig() BotConfig {
	return BotConfig{
		Enabled:      true,
		Persona:      "Pegasi, the AI assistant for PCS VoIP (Pegasus Communication Solutions).",
		Tone:         "warm, professional, knowledgeable",
		Greeting:     "Hi, I'm Pegasi — how can I help you today?",
		SystemPrompt: "You help website visitors understand PCS VoIP's products, services, pricing, and guide them toward the right communication solution. Keep answers concise.",
		Guardrails:   "Never invent prices. If unsure, suggest the visitor call 844-PCS-VOIP. Do not discuss competitors. Do not give legal, financial, or medical advice.",
		Voice:        "ara",
		MaxKBBytes:   30000,
		UpdatedAt:    time.Now().UTC(),
	}
}

// GetByKey returns a bot config by key (e.g. "outbound_aria").
func (r *BotRepo) GetByKey(key string) (*BotConfig, error) {
	var b BotConfig
	err := r.DB.Get(db.BucketBot, key, &b)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if b.Voice == "" {
		b.Voice = "ara"
	}
	return &b, nil
}

// SaveByKey persists a bot config under an arbitrary key.
func (r *BotRepo) SaveByKey(key string, b *BotConfig) error {
	b.UpdatedAt = time.Now().UTC()
	if b.MaxKBBytes <= 0 {
		b.MaxKBBytes = 30000
	}
	return r.DB.Put(db.BucketBot, key, b)
}

// DefaultOutboundConfig returns the seed config for the outbound callback persona.
func DefaultOutboundConfig() BotConfig {
	return BotConfig{
		Enabled: true,
		Persona: "Aria, a senior sales and scheduling representative at PCS VoIP — The Power of Communications Simplified.",
		Tone:    "warm, professional, knowledgeable, conversational",
		Greeting: "Hi! This is Aria from PCS VoIP returning your call. Thank you for your interest! How can I help you today?",
		SystemPrompt: `You are Aria, a senior sales and scheduling representative at PCS VoIP.

YOUR ROLE
1. Greet the caller by name and introduce yourself.
2. Ask how you can help them today.
3. Answer questions about PCS VoIP products and services.
4. Qualify the lead: ask about their business size, current phone system, pain points, and what they're looking for.
5. Schedule a follow-up appointment or demo with a PCS VoIP sales specialist if interested.
6. If you cannot answer a technical question, offer to have a specialist follow up.

PCS VOIP PRODUCTS & SERVICES
- Business VoIP Phone Systems (hosted PBX, unlimited calling, HD voice)
- UC Client (unified communications: messaging, presence, desktop/mobile app)
- Contact Center (ACD queues, wallboards, workforce management)
- SIP Trunking (bring your own PBX, save 50-70% on phone bills)
- E-Fax (cloud faxing, no hardware needed)
- Enterprise Cloud SMS
- Video Conferencing & Contact Sharing
- Call Recording
- Mobile Application
- DID numbers — local, toll-free, international
- Pegasi AI Products: AI Auto Attendant, AI Chatbot, AI Audiobot, AI CRM

KEY SELLING POINTS
- Nationwide provider — supports any business, any size
- Industries: manufacturing, healthcare, retail, transportation, financial services, and more
- No long-term contracts required
- 24/7 US-based support
- Free number porting from current provider
- Starts at $19.95/user/month
- Phone: 844-PCS-VOIP (844-727-8647)
- Address: 12195 S Strang Line Rd, Olathe, Kansas 66062

CONVERSATION GUIDELINES
- Keep responses concise — this is a phone call, not a document.
- Listen actively. Let the caller speak. Don't interrupt.
- If they mention a competitor, acknowledge it respectfully and highlight PCS VoIP advantages.
- Always offer next steps: schedule a demo, send pricing, connect with a specialist.
- Never pressure or hard-sell.
- End every call graciously.`,
		Guardrails: "Never invent prices beyond the base rate. If unsure, offer to email a detailed quote or have a specialist follow up. Do not give legal, financial, or medical advice. Do not discuss competitors negatively.",
		Voice:      "kore",
		MaxKBBytes: 30000,
		UpdatedAt:  time.Now().UTC(),
	}
}

// ComposeContext builds the full system context the bots use at runtime:
// persona + tone + system prompt + guardrails + active KB chunks (up to
// MaxKBBytes). Empty result means the bot is disabled.
func ComposeContext(bot *BotConfig, kb *KBRepo) (string, error) {
	if bot == nil || !bot.Enabled {
		return "", nil
	}
	var sb strings.Builder
	if bot.Persona != "" {
		sb.WriteString("PERSONA: ")
		sb.WriteString(bot.Persona)
		sb.WriteString("\n")
	}
	if bot.Tone != "" {
		sb.WriteString("TONE: ")
		sb.WriteString(bot.Tone)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	if bot.SystemPrompt != "" {
		sb.WriteString(bot.SystemPrompt)
		sb.WriteString("\n\n")
	}
	if bot.Guardrails != "" {
		sb.WriteString("GUARDRAILS:\n")
		sb.WriteString(bot.Guardrails)
		sb.WriteString("\n\n")
	}

	// Active KB documents up to budget.
	docs, err := kb.ListActive()
	if err != nil {
		return "", err
	}
	if len(docs) > 0 {
		sb.WriteString("KNOWLEDGE BASE:\n")
		used := 0
		for _, d := range docs {
			block := "--- " + d.Title + " ---\n" + strings.TrimSpace(d.Content) + "\n\n"
			if used+len(block) > bot.MaxKBBytes && used > 0 {
				break
			}
			sb.WriteString(block)
			used += len(block)
			if used >= bot.MaxKBBytes {
				break
			}
		}
	}
	return sb.String(), nil
}
