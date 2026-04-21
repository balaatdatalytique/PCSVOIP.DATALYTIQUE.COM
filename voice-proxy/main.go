package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	grokAPIKey    string
	contextPrompt string
	upgrader      = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	// Per-session chat history (keyed by session_id)
	chatSessions   = make(map[string][]chatMsg)
	chatSessionsMu sync.Mutex

	// Dynamic context fetched from main webserver (BOT_CONTEXT_URL).
	botContextURL    string
	visitorLogURL    string
	internalAPIToken string
	ctxCache         struct {
		mu        sync.Mutex
		data      botCtx
		fetchedAt time.Time
	}
)

// botCtx is what fetchBotContext returns: the system prompt body plus
// metadata pulled from response headers (voice, greeting). Cached together so
// every voice session sees the same snapshot.
type botCtx struct {
	text     string
	voice    string
	greeting string
}

const ctxCacheTTL = 5 * time.Second

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func main() {
	grokAPIKey = os.Getenv("GROK_API_KEY")
	if grokAPIKey == "" {
		log.Fatal("GROK_API_KEY environment variable is required")
	}

	// Dynamic admin module integration. When BOT_CONTEXT_URL is set, we fetch
	// the bot prompt + KB from the main webserver on each request (with a 60 s
	// in-memory cache). When unset we fall back to the legacy static file.
	botContextURL = strings.TrimSpace(os.Getenv("BOT_CONTEXT_URL"))
	visitorLogURL = strings.TrimSpace(os.Getenv("VISITOR_LOG_URL"))
	internalAPIToken = strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN"))

	contextPath := os.Getenv("CONTEXT_PATH")
	if contextPath == "" {
		contextPath = "/app/assets/data/pcsvoip-context.txt"
	}
	if data, err := os.ReadFile(contextPath); err != nil {
		log.Printf("INFO: legacy context file %s not loaded: %v", contextPath, err)
		contextPrompt = "You are Pegasi, the AI assistant for PCS VoIP."
	} else {
		contextPrompt = string(data)
	}

	if botContextURL != "" {
		log.Printf("voice-proxy: dynamic bot context enabled (BOT_CONTEXT_URL=%s)", botContextURL)
	} else {
		log.Printf("voice-proxy: using static context file %s", contextPath)
	}
	if visitorLogURL != "" {
		log.Printf("voice-proxy: visitor logging enabled (VISITOR_LOG_URL=%s)", visitorLogURL)
	}

	port := os.Getenv("VOICE_PROXY_PORT")
	if port == "" {
		port = "9081"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/voice", handleVoiceProxy)
	mux.HandleFunc("/api/chat", handleTextChat)
	mux.HandleFunc("/api/callback", handleCallbackRequest)
	mux.HandleFunc("/ws/audiofork/", handleAudioFork)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	log.Printf("Voice proxy listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// fetchBotContext returns the latest bot system prompt + voice. When
// BOT_CONTEXT_URL is configured the result is fetched from the main webserver
// and cached for ctxCacheTTL. Falls back to the legacy static context on any
// error so the bots never become unresponsive due to a control-plane outage.
func fetchBotContext() botCtx {
	if botContextURL == "" {
		return botCtx{text: contextPrompt}
	}
	ctxCache.mu.Lock()
	if !ctxCache.fetchedAt.IsZero() && time.Since(ctxCache.fetchedAt) < ctxCacheTTL && ctxCache.data.text != "" {
		out := ctxCache.data
		ctxCache.mu.Unlock()
		return out
	}
	ctxCache.mu.Unlock()

	req, _ := http.NewRequest("GET", botContextURL, nil)
	if internalAPIToken != "" {
		req.Header.Set("X-Internal-Token", internalAPIToken)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("voice-proxy: fetch context failed: %v (using cached/static)", err)
		ctxCache.mu.Lock()
		defer ctxCache.mu.Unlock()
		if ctxCache.data.text != "" {
			return ctxCache.data
		}
		return botCtx{text: contextPrompt}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		log.Printf("voice-proxy: context status %d (using cached/static)", resp.StatusCode)
		ctxCache.mu.Lock()
		defer ctxCache.mu.Unlock()
		if ctxCache.data.text != "" {
			return ctxCache.data
		}
		return botCtx{text: contextPrompt}
	}
	bc := botCtx{
		text:     string(body),
		voice:    resp.Header.Get("X-Bot-Voice"),
		greeting: resp.Header.Get("X-Bot-Greeting"),
	}
	ctxCache.mu.Lock()
	ctxCache.data = bc
	ctxCache.fetchedAt = time.Now()
	ctxCache.mu.Unlock()
	return bc
}

// logVisitorEvent fires-and-forgets a POST to the main webserver. Failures are
// logged at info level but never block the visitor reply.
func logVisitorEvent(evType, ip, ua, message, reply string) {
	if visitorLogURL == "" {
		return
	}
	go func() {
		body, _ := json.Marshal(map[string]string{
			"type": evType, "ip": ip, "ua": ua, "message": message, "reply": reply,
		})
		req, _ := http.NewRequest("POST", visitorLogURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if internalAPIToken != "" {
			req.Header.Set("X-Internal-Token", internalAPIToken)
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("voice-proxy: visitor log failed: %v", err)
			return
		}
		resp.Body.Close()
	}()
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}

// ===================== TEXT CHAT via Grok REST API =====================

func handleTextChat(w http.ResponseWriter, r *http.Request) {
	// CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")

	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", 400)
		return
	}

	// Generate session ID if not provided
	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("pegasi-%d", randID())
	}

	// Get or create chat history for this session
	chatSessionsMu.Lock()
	history, exists := chatSessions[req.SessionID]
	if !exists {
		history = []chatMsg{}
	}

	// Append user message
	history = append(history, chatMsg{Role: "user", Content: req.Message})

	// Keep history manageable (last 20 messages)
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	chatSessions[req.SessionID] = history
	chatSessionsMu.Unlock()

	// Build messages array for Grok using the latest dynamic context.
	bc := fetchBotContext()
	messages := []map[string]string{
		{"role": "system", "content": bc.text},
	}
	// Add user name context if provided
	if req.FirstName != "" {
		nameCtx := fmt.Sprintf("The visitor's name is %s %s.", req.FirstName, req.LastName)
		messages = append(messages, map[string]string{"role": "system", "content": nameCtx})
	}
	for _, m := range history {
		messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
	}

	// Call Grok REST API with tool-calling support for CoreDial KB search.
	reply, err := callGrokWithTools(messages)
	if err != nil {
		log.Printf("Grok API error: %v", err)
		writeChat(w, req.SessionID, "Sorry, I'm having trouble connecting right now. Please try again or call us at 844-PCS-VOIP.")
		return
	}

	// Store assistant reply in history
	chatSessionsMu.Lock()
	chatSessions[req.SessionID] = append(chatSessions[req.SessionID], chatMsg{Role: "assistant", Content: reply})
	chatSessionsMu.Unlock()

	// Log to admin module (best effort).
	logVisitorEvent("bot_chat", clientIP(r), r.UserAgent(), req.Message, reply)

	writeChat(w, req.SessionID, reply)
}

func writeChat(w http.ResponseWriter, sessionID, message string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"session_id": sessionID,
		"message":    message,
	})
}

var idCounter int64
var idMu sync.Mutex

func randID() int64 {
	idMu.Lock()
	defer idMu.Unlock()
	idCounter++
	return idCounter
}

// ===================== VOICE PROXY =====================

func handleVoiceProxy(w http.ResponseWriter, r *http.Request) {
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer clientConn.Close()

	// Voice resolution: the admin-configured voice from /api/bot/context wins,
	// then the explicit ?voice= URL parameter (only used when the admin hasn't
	// configured one), then "ara". The admin panel must be the source of truth
	// — operators expect their setting to take effect immediately.
	bc := fetchBotContext()
	voice := bc.voice
	if voice == "" {
		voice = r.URL.Query().Get("voice")
	}
	if voice == "" {
		voice = "ara"
	}

	// The xAI realtime API uses the format "human_Xxxx" for voice names
	// (e.g., "human_Baxter", "human_Eva"). Our admin stores lowercase ("baxter"),
	// so normalize before sending.
	grokVoice := normalizeVoice(voice)
	grokURL := "wss://api.x.ai/v1/realtime?model=grok-4-1-fast-non-reasoning&voice=" + grokVoice
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+grokAPIKey)

	grokConn, _, err := websocket.DefaultDialer.Dial(grokURL, headers)
	if err != nil {
		log.Printf("Failed to connect to Grok: %v", err)
		clientConn.WriteJSON(map[string]string{"type": "error", "error": "Failed to connect to voice service"})
		return
	}
	defer grokConn.Close()

	log.Printf("Voice session started (admin=%s, grok=%s)", voice, grokVoice)

	sessionUpdate := map[string]interface{}{
		"type": "session.update",
		"session": map[string]interface{}{
			"voice":        grokVoice,
			"instructions": bc.text,
			"turn_detection": map[string]interface{}{
				"type":                "server_vad",
				"threshold":           0.6,
				"silence_duration_ms": 1200,
				"prefix_padding_ms":   500,
			},
			"audio": map[string]interface{}{
				"input":  map[string]interface{}{"format": map[string]interface{}{"type": "audio/pcm", "rate": 24000}},
				"output": map[string]interface{}{"format": map[string]interface{}{"type": "audio/pcm", "rate": 24000}},
			},
			"input_audio_transcription": map[string]interface{}{
				"model": "grok-4-1-fast-non-reasoning",
			},
			"tools": []interface{}{coreDialToolDef},
		},
	}
	if err := grokConn.WriteJSON(sessionUpdate); err != nil {
		log.Printf("Failed to send session.update: %v", err)
		return
	}

	// Make the bot speak the greeting immediately so the visitor doesn't have
	// to talk first. We inject a user-role message asking for the greeting,
	// then trigger a response so Grok speaks it aloud in the configured voice.
	greeting := bc.greeting
	if greeting == "" {
		greeting = "Hi, I'm Pegasi — how can I help you today?"
	}
	grokConn.WriteJSON(map[string]interface{}{
		"type": "conversation.item.create",
		"item": map[string]interface{}{
			"type": "message",
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "input_text", "text": "Greet the visitor. Say exactly: " + greeting},
			},
		},
	})
	grokConn.WriteJSON(map[string]interface{}{
		"type": "response.create",
	})

	// Record the visitor interaction at session START so the count is captured
	// even if the connection later hangs, drops, or never sends a transcript.
	logVisitorEvent("bot_voice", clientIP(r), r.UserAgent(), "voice session started", "")

	var wg sync.WaitGroup
	var closeOnce sync.Once
	done := make(chan struct{})
	// closeAll is the single tear-down path. It closes both WebSocket
	// connections so any blocked ReadMessage() in either worker goroutine
	// returns immediately. Guarded by sync.Once so concurrent error paths
	// don't double-close.
	closeAll := func() {
		closeOnce.Do(func() {
			close(done)
			grokConn.Close()
			clientConn.Close()
		})
	}

	// Browser → Grok
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			msgType, msg, err := clientConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					select {
					case <-done:
					default:
						log.Printf("Client read error: %v", err)
					}
				}
				closeAll()
				return
			}
			if msgType == websocket.TextMessage {
				var envelope map[string]interface{}
				if json.Unmarshal(msg, &envelope) == nil {
					t, _ := envelope["type"].(string)
					switch {
					case t == "input_audio_buffer.append",
						t == "input_audio_buffer.commit",
						t == "input_audio_buffer.clear",
						t == "response.create",
						t == "response.cancel":
						grokConn.WriteMessage(websocket.TextMessage, msg)
					case strings.HasPrefix(t, "conversation."):
						grokConn.WriteMessage(websocket.TextMessage, msg)
					}
				}
			}
		}
	}()

	// Grok → Browser
	// Adaptive VAD: track speaker cadence and tune silence threshold.
	cadence := &cadenceTracker{}

	// Track in-progress function calls (voice tool use).
	voiceToolCalls := make(map[string]*voiceToolCall)
	var voiceToolMu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			msgType, msg, err := grokConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					select {
					case <-done:
					default:
						log.Printf("Grok read error: %v", err)
					}
				}
				closeAll()
				return
			}
			if msgType == websocket.TextMessage {
				var envelope map[string]interface{}
				if json.Unmarshal(msg, &envelope) == nil {
					t, _ := envelope["type"].(string)
					switch {
					case t == "session.created",
						t == "session.updated":
						log.Printf("Grok %s: %s", t, string(msg))
						clientConn.WriteMessage(websocket.TextMessage, msg)
					case t == "error":
						log.Printf("Grok error: %s", string(msg))
						clientConn.WriteMessage(websocket.TextMessage, msg)

					// ---- Function call handling for voice tool use ----
					case t == "response.function_call_arguments.delta":
						callID, _ := envelope["call_id"].(string)
						delta, _ := envelope["delta"].(string)
						voiceToolMu.Lock()
						tc, ok := voiceToolCalls[callID]
						if !ok {
							tc = &voiceToolCall{}
							voiceToolCalls[callID] = tc
						}
						tc.argsJSON += delta
						voiceToolMu.Unlock()

					case t == "response.function_call_arguments.done":
						callID, _ := envelope["call_id"].(string)
						name, _ := envelope["name"].(string)
						argsStr, _ := envelope["arguments"].(string)

						voiceToolMu.Lock()
						tc, ok := voiceToolCalls[callID]
						if ok {
							if argsStr == "" {
								argsStr = tc.argsJSON
							}
						}
						delete(voiceToolCalls, callID)
						voiceToolMu.Unlock()

						if name == "search_coredial_kb" || (ok && tc != nil) {
							go handleVoiceToolCall(grokConn, callID, name, argsStr)
						}

						// Tell browser we're searching
						clientConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created"}`))

					case t == "input_audio_buffer.speech_started":
						cadence.onSpeechStarted()
						clientConn.WriteMessage(websocket.TextMessage, msg)

					case t == "input_audio_buffer.speech_stopped":
						cadence.onSpeechStopped()
						if newSilence := cadence.recommend(); newSilence > 0 {
							grokConn.WriteJSON(map[string]interface{}{
								"type": "session.update",
								"session": map[string]interface{}{
									"turn_detection": map[string]interface{}{
										"type":                "server_vad",
										"threshold":           0.6,
										"silence_duration_ms": newSilence,
										"prefix_padding_ms":   500,
									},
								},
							})
						}
						clientConn.WriteMessage(websocket.TextMessage, msg)

					case t == "conversation.created",
						t == "response.output_audio.delta",
						t == "response.output_audio.done",
						t == "response.output_audio_transcript.delta",
						t == "response.text.delta",
						t == "response.done",
						t == "response.created",
						t == "input_audio_buffer.committed",
						t == "conversation.item.input_audio_transcription.completed":
						clientConn.WriteMessage(websocket.TextMessage, msg)
					}
				}
			}
		}
	}()

	wg.Wait()
	log.Printf("Voice session ended")
}

// ===================== ADAPTIVE CADENCE =====================

// cadenceTracker monitors speech_started / speech_stopped events to learn
// how the caller speaks. After enough samples it recommends a tuned
// silence_duration_ms so the bot waits longer for slow/deliberate speakers
// and responds faster for fluent ones.
type cadenceTracker struct {
	mu             sync.Mutex
	speechStart    time.Time     // last speech_started timestamp
	speechStop     time.Time     // last speech_stopped timestamp
	segments       []time.Duration // durations of speech segments
	gaps           []time.Duration // durations of pauses between segments
	lastRecommend  int           // last silence_duration_ms we sent
}

const (
	cadenceMinSamples = 3   // need this many gaps before adapting
	cadenceMinSilence = 800 // never go below 800ms
	cadenceMaxSilence = 2000 // never go above 2000ms
)

func (c *cadenceTracker) onSpeechStarted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	// If we have a previous speech_stopped, record the gap (pause duration)
	if !c.speechStop.IsZero() {
		gap := now.Sub(c.speechStop)
		// Only count realistic pauses (50ms–5s); ignore long silences
		if gap > 50*time.Millisecond && gap < 5*time.Second {
			c.gaps = append(c.gaps, gap)
			// Keep a sliding window of the last 10 gaps
			if len(c.gaps) > 10 {
				c.gaps = c.gaps[len(c.gaps)-10:]
			}
		}
	}
	c.speechStart = now
}

func (c *cadenceTracker) onSpeechStopped() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	// Record speech segment duration
	if !c.speechStart.IsZero() {
		seg := now.Sub(c.speechStart)
		if seg > 0 {
			c.segments = append(c.segments, seg)
			if len(c.segments) > 10 {
				c.segments = c.segments[len(c.segments)-10:]
			}
		}
	}
	c.speechStop = now
}

// recommend returns a new silence_duration_ms if the tracker has enough data
// and the value has changed meaningfully (>100ms). Returns 0 if no update needed.
func (c *cadenceTracker) recommend() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.gaps) < cadenceMinSamples {
		return 0
	}

	// Calculate average gap (natural pause duration for this speaker)
	var total time.Duration
	for _, g := range c.gaps {
		total += g
	}
	avgGap := total / time.Duration(len(c.gaps))

	// Set silence_duration to ~1.5x the speaker's average pause so the bot
	// doesn't jump in during their natural rhythm, but responds reasonably
	// quickly after they truly stop.
	recommended := int(avgGap.Milliseconds() * 3 / 2)

	// Clamp to safe range
	if recommended < cadenceMinSilence {
		recommended = cadenceMinSilence
	}
	if recommended > cadenceMaxSilence {
		recommended = cadenceMaxSilence
	}

	// Only send an update if meaningfully different from last recommendation
	if abs(recommended-c.lastRecommend) < 100 {
		return 0
	}

	c.lastRecommend = recommended
	log.Printf("cadence: adapted silence_duration_ms=%d (avg gap=%dms, %d samples)",
		recommended, avgGap.Milliseconds(), len(c.gaps))
	return recommended
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// voiceToolCall tracks a streaming function call from the Grok Realtime API.
type voiceToolCall struct {
	argsJSON string
}

// handleVoiceToolCall executes a tool call during a voice session and sends
// the result back to Grok so it can speak the answer.
func handleVoiceToolCall(grokConn *websocket.Conn, callID, name, argsJSON string) {
	log.Printf("coredial-voice: tool call %s(%s)", name, argsJSON)

	var result string
	if name == "search_coredial_kb" {
		var args struct {
			Query string `json:"query"`
		}
		json.Unmarshal([]byte(argsJSON), &args)
		if args.Query == "" {
			result = "No search query provided."
		} else {
			r, err := searchCoreDial(args.Query)
			if err != nil {
				result = fmt.Sprintf("Search failed: %v", err)
			} else {
				// Trim for voice context (keep it shorter than text chat)
				if len(r) > 3000 {
					r = r[:3000] + "\n... (truncated)"
				}
				result = r
			}
		}
	} else {
		result = fmt.Sprintf("Unknown tool: %s", name)
	}

	// Send function call output back to Grok
	grokConn.WriteJSON(map[string]interface{}{
		"type": "conversation.item.create",
		"item": map[string]interface{}{
			"type":    "function_call_output",
			"call_id": callID,
			"output":  result,
		},
	})
	// Trigger Grok to generate a spoken response from the tool result
	grokConn.WriteJSON(map[string]interface{}{
		"type": "response.create",
	})
}

// normalizeVoice converts an admin-friendly lowercase voice name into the
// format the xAI realtime API expects: "human_Xxxx" (e.g., "baxter" → "human_Baxter").
// If the voice already has the prefix or is empty, it is returned as-is.
func normalizeVoice(v string) string {
	if v == "" {
		return "human_Ara"
	}
	if strings.HasPrefix(v, "human_") {
		return v
	}
	return "human_" + strings.ToUpper(v[:1]) + v[1:]
}

// ===================== GROK TOOL-CALLING =====================

// callGrokWithTools sends messages to Grok with the CoreDial KB search tool
// defined. If Grok calls the tool, we execute the search and feed results
// back for a final answer. Supports up to 2 rounds of tool use.
func callGrokWithTools(messages []map[string]string) (string, error) {
	// Convert to the richer message format needed for tool calls
	type msgContent struct {
		Role       string      `json:"role"`
		Content    interface{} `json:"content,omitempty"`
		ToolCalls  interface{} `json:"tool_calls,omitempty"`
		ToolCallID string      `json:"tool_call_id,omitempty"`
	}

	var richMessages []msgContent
	for _, m := range messages {
		richMessages = append(richMessages, msgContent{Role: m["role"], Content: m["content"]})
	}

	for round := 0; round < 3; round++ {
		grokReq := map[string]interface{}{
			"model":       "grok-3-mini-fast",
			"messages":    richMessages,
			"max_tokens":  800,
			"temperature": 0.7,
			"tools":       []interface{}{coreDialToolDef},
		}
		body, _ := json.Marshal(grokReq)

		httpReq, _ := http.NewRequest("POST", "https://api.x.ai/v1/chat/completions", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+grokAPIKey)

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("grok API: %w", err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			return "", fmt.Errorf("grok API status %d: %s", resp.StatusCode, string(respBody))
		}

		var grokResp struct {
			Choices []struct {
				Message struct {
					Content   *string `json:"content"`
					ToolCalls []struct {
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(respBody, &grokResp); err != nil || len(grokResp.Choices) == 0 {
			return "", fmt.Errorf("grok parse error: %v", err)
		}

		choice := grokResp.Choices[0]

		// If no tool calls, return the text content
		if len(choice.Message.ToolCalls) == 0 {
			if choice.Message.Content != nil {
				return *choice.Message.Content, nil
			}
			return "Sorry, I couldn't generate a response. Please try again.", nil
		}

		// Process tool calls
		log.Printf("coredial: Grok requested %d tool call(s)", len(choice.Message.ToolCalls))

		// Add the assistant message with tool calls to the conversation
		richMessages = append(richMessages, msgContent{
			Role:      "assistant",
			ToolCalls: choice.Message.ToolCalls,
		})

		for _, tc := range choice.Message.ToolCalls {
			if tc.Function.Name == "search_coredial_kb" {
				// Parse the query argument
				var args struct {
					Query string `json:"query"`
				}
				json.Unmarshal([]byte(tc.Function.Arguments), &args)

				log.Printf("coredial: searching for %q", args.Query)
				result, err := searchCoreDial(args.Query)
				if err != nil {
					result = fmt.Sprintf("Search failed: %v", err)
					log.Printf("coredial: search error: %v", err)
				}

				// Add tool result to conversation
				richMessages = append(richMessages, msgContent{
					Role:       "tool",
					Content:    result,
					ToolCallID: tc.ID,
				})
			}
		}
		// Loop back to get Grok's final answer incorporating the search results
	}

	return "Sorry, I wasn't able to complete the search. Please try again.", nil
}

func init() {
	loadEnvFile(".env")
}

func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
			_ = fmt.Sprintf("")
		}
	}
}
