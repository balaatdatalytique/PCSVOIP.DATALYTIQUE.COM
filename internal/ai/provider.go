package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// AIProvider is the interface all AI providers must implement.
type AIProvider interface {
	GenerateContent(prompt string, context string) (string, error)
}

// ProviderConfig holds configuration for creating an AI provider.
type ProviderConfig struct {
	Provider string
	APIKey   string
	Model    string
}

// NewProvider creates an AI provider based on config.
func NewProvider(cfg ProviderConfig) (AIProvider, error) {
	switch cfg.Provider {
	case "openai":
		model := cfg.Model
		if model == "" {
			model = "gpt-4"
		}
		return &OpenAIProvider{apiKey: cfg.APIKey, model: model}, nil
	case "anthropic":
		model := cfg.Model
		if model == "" {
			model = "claude-sonnet-4-20250514"
		}
		return &AnthropicProvider{apiKey: cfg.APIKey, model: model}, nil
	case "grok":
		model := cfg.Model
		if model == "" {
			model = "grok-4-1-fast-non-reasoning"
		}
		return &GrokProvider{apiKey: cfg.APIKey, model: model}, nil
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s (supported: openai, anthropic, grok)", cfg.Provider)
	}
}

// cmsSystemPrompt is the shared guardrail system prompt for all AI providers.
// Uses a diff-based approach: AI returns JSON find/replace operations instead of the full file.
const cmsSystemPrompt = `You are a surgical HTML/CSS editor for a live production website. You receive a file and an editing instruction. You must return ONLY a JSON array of find-and-replace operations.

RESPONSE FORMAT — return ONLY valid JSON, nothing else:
[
  {"find": "exact string to find in the file", "replace": "replacement string"},
  {"find": "another exact string", "replace": "its replacement"}
]

CRITICAL RULES:
1. Each "find" value must be an EXACT substring that exists in the provided file content — copied character-for-character including whitespace and newlines.
2. Each "find" must be unique in the file (include enough surrounding context to be unambiguous).
3. ONLY include operations that directly fulfill the user's instruction. Do NOT touch anything else.
4. NEVER change file paths, image src, href, link href, or script src attributes.
5. NEVER remove or restructure HTML elements, IDs, classes, or attributes not mentioned in the instruction.
6. NEVER add external dependencies or CDN links unless explicitly requested.
7. Keep operations minimal — prefer the fewest, smallest replacements that achieve the goal.
8. If the edit requires adding new CSS, use a find/replace that targets an existing <style> block or the </head> tag to inject a <style> block before it.
9. Return ONLY the JSON array. No markdown fences, no explanations, no commentary.
10. If the instruction cannot be fulfilled, return an empty array: []`

// buildUserPrompt wraps the user instruction with the file content.
func buildUserPrompt(instruction, content string) string {
	return fmt.Sprintf("INSTRUCTION: %s\n\nFILE CONTENT:\n%s", instruction, content)
}

// ApplyEdits applies a list of find/replace operations to content.
// Returns the modified content or an error if a find string is not found.
// Handles line-ending mismatches: AI often returns \n but files may use \r\n.
func ApplyEdits(content string, edits []map[string]string) (string, error) {
	result := content
	for i, edit := range edits {
		find := edit["find"]
		replace := edit["replace"]
		if find == "" {
			continue
		}

		// Try exact match first
		if strings.Contains(result, find) {
			result = strings.Replace(result, find, replace, 1)
			continue
		}

		// If file uses \r\n but AI returned \n, convert find/replace to match
		if strings.Contains(result, "\r\n") && !strings.Contains(find, "\r\n") {
			crlfFind := strings.ReplaceAll(find, "\n", "\r\n")
			crlfReplace := strings.ReplaceAll(replace, "\n", "\r\n")
			if strings.Contains(result, crlfFind) {
				result = strings.Replace(result, crlfFind, crlfReplace, 1)
				continue
			}
		}

		// If file uses \n but AI returned \r\n, convert find/replace to match
		if !strings.Contains(result, "\r\n") && strings.Contains(find, "\r\n") {
			lfFind := strings.ReplaceAll(find, "\r\n", "\n")
			lfReplace := strings.ReplaceAll(replace, "\r\n", "\n")
			if strings.Contains(result, lfFind) {
				result = strings.Replace(result, lfFind, lfReplace, 1)
				continue
			}
		}

		// Try whitespace-normalized match as last resort
		normResult := normalizeWhitespace(result)
		normFind := normalizeWhitespace(find)
		if idx := strings.Index(normResult, normFind); idx != -1 {
			// Find the corresponding span in the original content
			origStart, origEnd := mapNormalizedIndex(result, idx, len(normFind))
			if origStart >= 0 && origEnd >= 0 {
				result = result[:origStart] + replace + result[origEnd:]
				continue
			}
		}

		return "", fmt.Errorf("edit %d: could not find target string in file (first 80 chars: %q)", i+1, truncate(find, 80))
	}
	return result, nil
}

// normalizeWhitespace collapses all runs of whitespace (spaces, tabs, newlines) into a single space.
func normalizeWhitespace(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
		} else {
			b.WriteRune(r)
			inSpace = false
		}
	}
	return b.String()
}

// mapNormalizedIndex maps a (start, length) in the normalized string back to (start, end) in the original.
func mapNormalizedIndex(original string, normIdx, normLen int) (int, int) {
	normPos := 0
	origStart := -1
	inSpace := false
	i := 0
	runes := []byte(original)

	for i < len(runes) && normPos < normIdx+normLen {
		ch := runes[i]
		isWS := ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'

		if isWS {
			if !inSpace {
				if normPos == normIdx {
					origStart = i
				}
				normPos++
				inSpace = true
			}
			i++
		} else {
			if normPos == normIdx {
				origStart = i
			}
			inSpace = false
			normPos++
			i++
		}
	}

	if origStart < 0 {
		return -1, -1
	}
	return origStart, i
}

// ParseEditsResponse parses the AI response as a JSON array of {find, replace} objects.
func ParseEditsResponse(response string) ([]map[string]string, error) {
	response = stripCodeFences(response)
	response = strings.TrimSpace(response)

	var edits []map[string]string
	if err := json.Unmarshal([]byte(response), &edits); err != nil {
		return nil, fmt.Errorf("AI returned invalid JSON: %w (response: %s)", err, truncate(response, 200))
	}

	if len(edits) == 0 {
		return nil, fmt.Errorf("AI returned no edits — instruction may not be applicable to this file")
	}

	// Validate each edit has find and replace keys
	for i, edit := range edits {
		if _, ok := edit["find"]; !ok {
			return nil, fmt.Errorf("edit %d missing 'find' key", i+1)
		}
		if _, ok := edit["replace"]; !ok {
			return nil, fmt.Errorf("edit %d missing 'replace' key", i+1)
		}
	}

	return edits, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- OpenAI Provider ---

type OpenAIProvider struct {
	apiKey string
	model  string
}

func (p *OpenAIProvider) GenerateContent(prompt, context string) (string, error) {
	if p.apiKey == "" {
		return "", fmt.Errorf("OpenAI API key not configured (set CMS_AI_API_KEY)")
	}

	body := map[string]interface{}{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": cmsSystemPrompt},
			{"role": "user", "content": buildUserPrompt(prompt, context)},
		},
		"temperature": 0.3,
		"max_tokens":  16384,
	}

	return postJSON("https://api.openai.com/v1/chat/completions", p.apiKey, body, func(resp map[string]interface{}) (string, error) {
		choices, ok := resp["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			return "", fmt.Errorf("no response from OpenAI")
		}
		msg := choices[0].(map[string]interface{})["message"].(map[string]interface{})
		return stripCodeFences(msg["content"].(string)), nil
	})
}

// --- Anthropic Provider ---

type AnthropicProvider struct {
	apiKey string
	model  string
}

func (p *AnthropicProvider) GenerateContent(prompt, context string) (string, error) {
	if p.apiKey == "" {
		return "", fmt.Errorf("Anthropic API key not configured (set CMS_AI_API_KEY)")
	}

	body := map[string]interface{}{
		"model":      p.model,
		"max_tokens": 16384,
		"system":     cmsSystemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": buildUserPrompt(prompt, context)},
		},
	}

	req, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(req))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 120 * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("Anthropic API error: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", err
	}

	if httpResp.StatusCode != 200 {
		return "", fmt.Errorf("Anthropic API error %d: %s", httpResp.StatusCode, string(respBody))
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", err
	}

	content, ok := resp["content"].([]interface{})
	if !ok || len(content) == 0 {
		return "", fmt.Errorf("no response from Anthropic")
	}
	return stripCodeFences(content[0].(map[string]interface{})["text"].(string)), nil
}

// --- Grok Provider ---

type GrokProvider struct {
	apiKey string
	model  string
}

func (p *GrokProvider) GenerateContent(prompt, context string) (string, error) {
	if p.apiKey == "" {
		return "", fmt.Errorf("Grok API key not configured (set CMS_AI_API_KEY)")
	}

	body := map[string]interface{}{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": cmsSystemPrompt},
			{"role": "user", "content": buildUserPrompt(prompt, context)},
		},
		"temperature": 0.3,
		"max_tokens":  16384,
	}

	return postJSON("https://api.x.ai/v1/chat/completions", p.apiKey, body, func(resp map[string]interface{}) (string, error) {
		choices, ok := resp["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			return "", fmt.Errorf("no response from Grok")
		}
		msg := choices[0].(map[string]interface{})["message"].(map[string]interface{})
		return stripCodeFences(msg["content"].(string)), nil
	})
}

// stripCodeFences removes markdown code fences (```html ... ```) from AI responses.
var codeFenceFullRe = regexp.MustCompile("(?s)^\\s*```[a-zA-Z]*\\n(.*?)\\n```\\s*$")
var codeFenceOpenRe = regexp.MustCompile("^\\s*```[a-zA-Z]*\\n")

func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	// Try full fence pair first
	if m := codeFenceFullRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	// Strip opening fence if closing is missing (truncated response)
	if loc := codeFenceOpenRe.FindStringIndex(s); loc != nil {
		s = s[loc[1]:]
	}
	// Strip trailing fence if present
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSpace(s[:len(s)-3])
	}
	return s
}

// --- Helpers ---

func postJSON(url, apiKey string, body interface{}, extract func(map[string]interface{}) (string, error)) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}

	return extract(result)
}
