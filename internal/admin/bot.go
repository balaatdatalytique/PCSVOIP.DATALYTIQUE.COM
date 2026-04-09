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

// Get returns the current bot config, defaulting on first call.
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
		MaxKBBytes:   30000,
		UpdatedAt:    time.Now().UTC(),
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
