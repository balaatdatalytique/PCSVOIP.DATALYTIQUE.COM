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
		text      string
		fetchedAt time.Time
	}
)

const ctxCacheTTL = 60 * time.Second

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
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	log.Printf("Voice proxy listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// fetchBotContext returns the latest bot system prompt. When BOT_CONTEXT_URL
// is configured the result is fetched from the main webserver and cached for
// ctxCacheTTL. Falls back to the legacy contextPrompt on any error so the
// bots never become unresponsive due to a control-plane outage.
func fetchBotContext() string {
	if botContextURL == "" {
		return contextPrompt
	}
	ctxCache.mu.Lock()
	if !ctxCache.fetchedAt.IsZero() && time.Since(ctxCache.fetchedAt) < ctxCacheTTL && ctxCache.text != "" {
		out := ctxCache.text
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
		if ctxCache.text != "" {
			return ctxCache.text
		}
		return contextPrompt
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		log.Printf("voice-proxy: context status %d (using cached/static)", resp.StatusCode)
		if ctxCache.text != "" {
			return ctxCache.text
		}
		return contextPrompt
	}
	text := string(body)
	ctxCache.mu.Lock()
	ctxCache.text = text
	ctxCache.fetchedAt = time.Now()
	ctxCache.mu.Unlock()
	return text
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
	currentContext := fetchBotContext()
	messages := []map[string]string{
		{"role": "system", "content": currentContext},
	}
	// Add user name context if provided
	if req.FirstName != "" {
		nameCtx := fmt.Sprintf("The visitor's name is %s %s.", req.FirstName, req.LastName)
		messages = append(messages, map[string]string{"role": "system", "content": nameCtx})
	}
	for _, m := range history {
		messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
	}

	// Call Grok REST API
	grokReq := map[string]interface{}{
		"model":       "grok-3-mini-fast",
		"messages":    messages,
		"max_tokens":  500,
		"temperature": 0.7,
	}
	body, _ := json.Marshal(grokReq)

	httpReq, _ := http.NewRequest("POST", "https://api.x.ai/v1/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+grokAPIKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		log.Printf("Grok API error: %v", err)
		writeChat(w, req.SessionID, "Sorry, I'm having trouble connecting right now. Please try again or call us at 844-PCS-VOIP.")
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		log.Printf("Grok API status %d: %s", resp.StatusCode, string(respBody))
		writeChat(w, req.SessionID, "Sorry, I'm having trouble right now. Please call us at 844-PCS-VOIP for immediate help.")
		return
	}

	var grokResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &grokResp); err != nil || len(grokResp.Choices) == 0 {
		log.Printf("Grok response parse error: %v", err)
		writeChat(w, req.SessionID, "Sorry, something went wrong. Please try again.")
		return
	}

	reply := grokResp.Choices[0].Message.Content

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

	voice := r.URL.Query().Get("voice")
	if voice == "" {
		voice = "ara"
	}

	grokURL := "wss://api.x.ai/v1/realtime?model=grok-4-1-fast-non-reasoning"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+grokAPIKey)

	grokConn, _, err := websocket.DefaultDialer.Dial(grokURL, headers)
	if err != nil {
		log.Printf("Failed to connect to Grok: %v", err)
		clientConn.WriteJSON(map[string]string{"type": "error", "error": "Failed to connect to voice service"})
		return
	}
	defer grokConn.Close()

	log.Printf("Voice session started (voice=%s)", voice)

	voiceContext := fetchBotContext()
	sessionUpdate := map[string]interface{}{
		"type": "session.update",
		"session": map[string]interface{}{
			"voice":        voice,
			"instructions": voiceContext,
			"turn_detection": map[string]interface{}{
				"type":                "server_vad",
				"threshold":           0.85,
				"silence_duration_ms": 600,
				"prefix_padding_ms":   333,
			},
			"audio": map[string]interface{}{
				"input":  map[string]interface{}{"format": map[string]interface{}{"type": "audio/pcm", "rate": 24000}},
				"output": map[string]interface{}{"format": map[string]interface{}{"type": "audio/pcm", "rate": 24000}},
			},
			"input_audio_transcription": map[string]interface{}{
				"model": "grok-4-1-fast-non-reasoning",
			},
		},
	}
	if err := grokConn.WriteJSON(sessionUpdate); err != nil {
		log.Printf("Failed to send session.update: %v", err)
		return
	}

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
						t == "session.updated",
						t == "conversation.created",
						t == "error",
						t == "response.output_audio.delta",
						t == "response.output_audio.done",
						t == "response.output_audio_transcript.delta",
						t == "response.text.delta",
						t == "response.done",
						t == "response.created",
						t == "input_audio_buffer.speech_started",
						t == "input_audio_buffer.speech_stopped",
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
