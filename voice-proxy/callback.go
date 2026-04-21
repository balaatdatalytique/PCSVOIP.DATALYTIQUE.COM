package main

// callback.go — Outbound AI callback via FreeSWITCH ESL + mod_audio_fork.
//
// Flow:
//   1. POST /api/callback → originate call via FreeSWITCH ESL
//   2. FreeSWITCH calls the number, on answer runs uuid_audio_fork → /ws/audiofork/{session}
//   3. /ws/audiofork/{session} bridges FreeSWITCH PCM16/8kHz ↔ Grok Realtime PCM16/24kHz

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ===================== ESL CLIENT (minimal) =====================

type eslClient struct {
	host, password string
	port           int
	mu             sync.Mutex
	conn           net.Conn
	reader         *textproto.Reader
	connected      bool
}

func newESLClient() *eslClient {
	port := 8021
	fmt.Sscanf(os.Getenv("FREESWITCH_ESL_PORT"), "%d", &port)
	return &eslClient{
		host:     envOr("FREESWITCH_HOST", "host.docker.internal"),
		port:     port,
		password: os.Getenv("FREESWITCH_ESL_PASSWORD"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *eslClient) connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("esl dial %s: %w", addr, err)
	}
	c.conn = conn
	c.reader = textproto.NewReader(bufio.NewReader(conn))
	// Read initial auth/request
	if _, err := c.readResp(); err != nil {
		conn.Close()
		return err
	}
	// Authenticate
	if _, err := conn.Write([]byte(fmt.Sprintf("auth %s\n\n", c.password))); err != nil {
		conn.Close()
		return err
	}
	resp, err := c.readResp()
	if err != nil || !strings.Contains(resp, "+OK") {
		conn.Close()
		return fmt.Errorf("esl auth failed: %s %v", resp, err)
	}
	c.connected = true
	log.Printf("esl: connected to %s", addr)
	return nil
}

func (c *eslClient) api(cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return "", fmt.Errorf("esl not connected")
	}
	if _, err := c.conn.Write([]byte(fmt.Sprintf("api %s\n\n", cmd))); err != nil {
		c.connected = false
		return "", err
	}
	return c.readResp()
}

func (c *eslClient) bgapi(cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return "", fmt.Errorf("esl not connected")
	}
	if _, err := c.conn.Write([]byte(fmt.Sprintf("bgapi %s\n\n", cmd))); err != nil {
		c.connected = false
		return "", err
	}
	return c.readResp()
}

func (c *eslClient) readResp() (string, error) {
	var contentLength int
	var headerLines []string
	for {
		line, err := c.reader.ReadLine()
		if err != nil {
			return "", err
		}
		if line == "" {
			break
		}
		headerLines = append(headerLines, line)
		if strings.HasPrefix(line, "Content-Length: ") {
			fmt.Sscanf(line[16:], "%d", &contentLength)
		}
	}
	allHeaders := strings.Join(headerLines, "\n")
	if contentLength > 0 {
		buf := make([]byte, contentLength)
		n := 0
		for n < contentLength {
			r, err := c.reader.R.Read(buf[n:])
			if err != nil {
				return "", err
			}
			n += r
		}
		return allHeaders + "\n" + string(buf), nil
	}
	return allHeaders, nil
}

func (c *eslClient) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.connected = false
}

// ===================== CALLBACK HANDLER =====================

var (
	esl              *eslClient
	eslOnce          sync.Once
	pendingSessions  = make(map[string]*callbackSession)
	pendingMu        sync.Mutex
	fsAudioForkBase  string // e.g. "ws://pcsvoip-voice:9081/ws/audiofork"
)

type callbackSession struct {
	ID        string
	FirstName string
	LastName  string
	Phone     string
	CreatedAt time.Time
}

func getESL() *eslClient {
	eslOnce.Do(func() {
		esl = newESLClient()
	})
	return esl
}

// handleCallbackRequest handles POST /api/callback
func handleCallbackRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		json.NewEncoder(w).Encode(map[string]string{"error": "Method not allowed"})
		return
	}

	var req struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Phone     string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid request"})
		return
	}
	if req.Phone == "" || req.FirstName == "" {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Name and phone are required"})
		return
	}

	// Normalize phone to E.164
	phone := normalizePhone(req.Phone)

	// Generate session ID
	sessionID := fmt.Sprintf("cb-%d", time.Now().UnixNano())

	// Store pending session
	sess := &callbackSession{
		ID:        sessionID,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Phone:     phone,
		CreatedAt: time.Now(),
	}
	pendingMu.Lock()
	pendingSessions[sessionID] = sess
	pendingMu.Unlock()

	// Clean up after 5 minutes if not used
	go func() {
		time.Sleep(5 * time.Minute)
		pendingMu.Lock()
		delete(pendingSessions, sessionID)
		pendingMu.Unlock()
	}()

	// Connect to FreeSWITCH ESL
	client := getESL()
	if err := client.connect(); err != nil {
		log.Printf("callback: ESL connect error: %v", err)
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "Call service unavailable. We'll follow up shortly."})
		return
	}

	// Build the audio fork WebSocket URL that FreeSWITCH will connect to
	if fsAudioForkBase == "" {
		fsAudioForkBase = envOr("AUDIO_FORK_WS_URL", "ws://localhost:9081/ws/audiofork")
	}
	wsURL := fmt.Sprintf("%s/%s", fsAudioForkBase, sessionID)

	// Originate the call — simple playback, audio_fork attached separately after answer
	callerID := strings.TrimPrefix(envOr("CALLBACK_CALLER_ID", "18447278647"), "+")
	gateway := envOr("FREESWITCH_GATEWAY", "coredial_east")
	cmd := fmt.Sprintf(
		"originate {origination_caller_id_number=%s,origination_caller_id_name=PCSVoIP,ignore_early_media=true,audio_fork_playback_direction=write}sofia/gateway/%s/%s &playback(silence_stream://-1)",
		callerID, gateway, phone,
	)

	log.Printf("callback: originating call to %s (session %s)", phone, sessionID)
	resp, err := client.bgapi(cmd)
	if err != nil {
		log.Printf("callback: originate error: %v", err)
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "Could not place the call. Please try again."})
		return
	}
	log.Printf("callback: originate response: %s", resp)

	// Background: poll for the channel to appear and attach audio_fork once answered
	go func() {
		// Wait for the call to be answered (poll every 500ms for up to 60s)
		var uuid string
		for i := 0; i < 120; i++ {
			time.Sleep(500 * time.Millisecond)
			channels, err := client.api("show channels as json")
			if err != nil {
				continue
			}
			// Parse JSON to find our call by destination number
			var result struct {
				Rows []struct {
					UUID      string `json:"uuid"`
					Dest      string `json:"dest"`
					Callstate string `json:"callstate"`
				} `json:"rows"`
			}
			// The response has headers + body, extract the JSON part
			jsonStart := strings.Index(channels, "{")
			if jsonStart < 0 {
				continue
			}
			if err := json.Unmarshal([]byte(channels[jsonStart:]), &result); err != nil {
				continue
			}
			for _, row := range result.Rows {
				if row.Dest == phone && (row.Callstate == "ACTIVE" || row.Callstate == "EARLY") {
					uuid = row.UUID
					break
				}
			}
			if uuid != "" {
				break
			}
		}
		if uuid == "" {
			log.Printf("callback: call to %s never answered or not found", phone)
			return
		}
		log.Printf("callback: call answered, uuid=%s — attaching audio_fork to %s", uuid, wsURL)

		// Set playback direction so mod_audio_fork injects AI audio into the WRITE path
		client.api(fmt.Sprintf("uuid_setvar %s audio_fork_playback_direction write", uuid))

		// Small delay to let the call stabilize
		time.Sleep(500 * time.Millisecond)

		// Attach mod_audio_fork
		forkCmd := fmt.Sprintf("uuid_audio_fork %s start %s mono 8000", uuid, wsURL)
		forkResp, err := client.api(forkCmd)
		if err != nil {
			log.Printf("callback: audio_fork error: %v", err)
			return
		}
		log.Printf("callback: audio_fork response: %s", forkResp)
	}()

	json.NewEncoder(w).Encode(map[string]string{
		"ok":         "Calling you now! Aria will be on the line shortly.",
		"session_id": sessionID,
	})
}

func normalizePhone(phone string) string {
	// Strip everything except digits
	var b strings.Builder
	for _, c := range phone {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	p := b.String()
	// Ensure 11-digit US format: 1XXXXXXXXXX (no + prefix for CoreDial)
	if len(p) == 10 {
		p = "1" + p
	}
	return p
}

// fetchOutboundContext fetches the outbound callback persona from the admin module.
func fetchOutboundContext() botCtx {
	url := strings.TrimSpace(os.Getenv("BOT_CONTEXT_URL"))
	if url == "" {
		return fetchBotContext() // fallback
	}
	// Replace /api/bot/context with /api/bot/outbound
	url = strings.Replace(url, "/api/bot/context", "/api/bot/outbound", 1)

	req, _ := http.NewRequest("GET", url, nil)
	if internalAPIToken != "" {
		req.Header.Set("X-Internal-Token", internalAPIToken)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("callback: fetch outbound context failed: %v", err)
		return fetchBotContext()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		log.Printf("callback: outbound context status %d", resp.StatusCode)
		return fetchBotContext()
	}
	return botCtx{
		text:     string(body),
		voice:    resp.Header.Get("X-Bot-Voice"),
		greeting: resp.Header.Get("X-Bot-Greeting"),
	}
}

// ===================== AUDIO FORK WEBSOCKET BRIDGE =====================
// FreeSWITCH mod_audio_fork connects here. We bridge to Grok Realtime API.

func handleAudioFork(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from URL: /ws/audiofork/{sessionID}
	parts := strings.Split(r.URL.Path, "/")
	sessionID := ""
	if len(parts) >= 4 {
		sessionID = parts[3]
	}
	if sessionID == "" {
		http.Error(w, "missing session", 400)
		return
	}

	// Look up pending session
	pendingMu.Lock()
	sess, ok := pendingSessions[sessionID]
	pendingMu.Unlock()

	callerName := "the caller"
	if ok && sess != nil {
		callerName = sess.FirstName + " " + sess.LastName
	}

	// Upgrade WebSocket (FreeSWITCH → us)
	fsConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("audiofork: ws upgrade failed: %v", err)
		return
	}
	defer fsConn.Close()
	log.Printf("audiofork: FreeSWITCH connected (session %s, caller %s)", sessionID, callerName)

	// Connect to Grok Realtime API — use outbound persona's voice
	bc := fetchBotContext() // fallback
	ob := fetchOutboundContext()
	voice := ob.voice
	if voice == "" {
		voice = bc.voice
	}
	if voice == "" {
		voice = "kore"
	}
	grokVoice := normalizeVoice(voice)
	grokURL := "wss://api.x.ai/v1/realtime?model=grok-4-1-fast-non-reasoning&voice=" + grokVoice
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+grokAPIKey)

	grokConn, _, err := websocket.DefaultDialer.Dial(grokURL, headers)
	if err != nil {
		log.Printf("audiofork: grok connect failed: %v", err)
		return
	}
	defer grokConn.Close()
	log.Printf("audiofork: Grok connected (voice=%s)", grokVoice)

	// Build callback prompt from the outbound persona (already fetched above for voice).
	callbackPrompt := ob.text + fmt.Sprintf("\n\nIMPORTANT: You are calling %s back. They requested this callback from the PCS VoIP website. Greet them by name.", callerName)

	// Configure Grok session
	sessionUpdate := map[string]interface{}{
		"type": "session.update",
		"session": map[string]interface{}{
			"voice":        grokVoice,
			"instructions": callbackPrompt,
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
	grokConn.WriteJSON(sessionUpdate)

	// Trigger greeting from persona config, personalized with caller name
	greeting := ob.greeting
	if greeting == "" {
		greeting = "Hi! This is Aria from PCS VoIP returning your call. How can I help you today?"
	}
	// Inject caller's first name
	if sess != nil && sess.FirstName != "" {
		greeting = strings.Replace(greeting, "Hi!", "Hi "+sess.FirstName+"!", 1)
	}
	grokConn.WriteJSON(map[string]interface{}{
		"type": "conversation.item.create",
		"item": map[string]interface{}{
			"type": "message",
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "input_text", "text": "Greet the caller. Say exactly: " + greeting},
			},
		},
	})
	grokConn.WriteJSON(map[string]interface{}{"type": "response.create"})

	// Audio bridge variables
	var (
		wg        sync.WaitGroup
		closeOnce sync.Once
		done      = make(chan struct{})
		// Audio buffer for smoother playback to FreeSWITCH
		audioBuffer   []byte
		audioBufferMu sync.Mutex
		lastSend      time.Time
	)
	closeAll := func() {
		closeOnce.Do(func() {
			close(done)
			fsConn.Close()
			grokConn.Close()
		})
	}

	// FreeSWITCH → Grok (caller audio)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			msgType, msg, err := fsConn.ReadMessage()
			if err != nil {
				select {
				case <-done:
				default:
					log.Printf("audiofork: fs read error: %v", err)
				}
				closeAll()
				return
			}
			// mod_audio_fork sends binary PCM16/8kHz frames
			if msgType == websocket.BinaryMessage && len(msg) > 0 {
				// Upsample 8kHz → 24kHz
				upsampled := resample8kTo24k(msg)
				b64 := base64.StdEncoding.EncodeToString(upsampled)
				grokConn.WriteJSON(map[string]interface{}{
					"type":  "input_audio_buffer.append",
					"audio": b64,
				})
			}
		}
	}()

	// Grok → FreeSWITCH (AI audio)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			_, msg, err := grokConn.ReadMessage()
			if err != nil {
				select {
				case <-done:
				default:
					log.Printf("audiofork: grok read error: %v", err)
				}
				closeAll()
				return
			}
			var envelope map[string]interface{}
			if json.Unmarshal(msg, &envelope) != nil {
				continue
			}
			t, _ := envelope["type"].(string)
			switch t {
			case "response.output_audio.delta":
				delta, _ := envelope["delta"].(string)
				if delta == "" {
					continue
				}
				// Decode base64 PCM16/24kHz from Grok
				audio, err := base64.StdEncoding.DecodeString(delta)
				if err != nil {
					continue
				}
				// Downsample 24kHz → 8kHz
				resampled := resample24kTo8k(audio)

				// Buffer and send in ~40ms chunks (640 bytes @ 8kHz)
				audioBufferMu.Lock()
				audioBuffer = append(audioBuffer, resampled...)
				shouldSend := len(audioBuffer) >= 640 ||
					(len(audioBuffer) > 0 && time.Since(lastSend) > 30*time.Millisecond)
				if shouldSend {
					toSend := audioBuffer
					audioBuffer = nil
					lastSend = time.Now()
					audioBufferMu.Unlock()

					b64 := base64.StdEncoding.EncodeToString(toSend)
					fsConn.WriteJSON(map[string]interface{}{
						"type": "playAudio",
						"data": map[string]interface{}{
							"audioContentType": "raw",
							"sampleRate":       8000,
							"audioContent":     b64,
						},
					})
				} else {
					audioBufferMu.Unlock()
				}

			case "input_audio_buffer.speech_started":
				// Barge-in: clear audio buffer and tell FS to stop playback
				audioBufferMu.Lock()
				audioBuffer = nil
				audioBufferMu.Unlock()
				fsConn.WriteJSON(map[string]interface{}{"type": "killAudio"})

			case "response.function_call_arguments.done":
				// Handle tool calls (CoreDial KB search)
				callID, _ := envelope["call_id"].(string)
				name, _ := envelope["name"].(string)
				argsStr, _ := envelope["arguments"].(string)
				if name == "search_coredial_kb" {
					go handleVoiceToolCall(grokConn, callID, name, argsStr)
				}

			case "error":
				log.Printf("audiofork: grok error: %s", string(msg))
			}
		}
	}()

	wg.Wait()
	log.Printf("audiofork: session %s ended", sessionID)

	// Clean up session
	pendingMu.Lock()
	delete(pendingSessions, sessionID)
	pendingMu.Unlock()
}

// ===================== AUDIO RESAMPLING =====================

// resample8kTo24k upsamples PCM16 mono from 8kHz to 24kHz via linear interpolation.
func resample8kTo24k(input []byte) []byte {
	if len(input) < 2 {
		return input
	}
	numSamples := len(input) / 2
	outputSamples := numSamples * 3
	output := make([]byte, outputSamples*2)

	samples := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		samples[i] = int16(input[i*2]) | int16(input[i*2+1])<<8
	}

	for i := 0; i < numSamples; i++ {
		current := int32(samples[i])
		var next int32
		if i+1 < numSamples {
			next = int32(samples[i+1])
		} else {
			next = current
		}
		outIdx := i * 3
		s0 := int16(current)
		s1 := int16(current + (next-current)/3)
		s2 := int16(current + 2*(next-current)/3)

		output[outIdx*2] = byte(s0)
		output[outIdx*2+1] = byte(s0 >> 8)
		output[(outIdx+1)*2] = byte(s1)
		output[(outIdx+1)*2+1] = byte(s1 >> 8)
		output[(outIdx+2)*2] = byte(s2)
		output[(outIdx+2)*2+1] = byte(s2 >> 8)
	}
	return output
}

// resample24kTo8k downsamples PCM16 mono from 24kHz to 8kHz via decimation with averaging.
func resample24kTo8k(input []byte) []byte {
	if len(input) < 2 {
		return input
	}
	numSamples := len(input) / 2
	outputSamples := numSamples / 3
	output := make([]byte, outputSamples*2)

	samples := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		samples[i] = int16(input[i*2]) | int16(input[i*2+1])<<8
	}

	for i := 0; i < outputSamples; i++ {
		srcIdx := i * 3
		var sum int32
		count := 0
		for j := 0; j < 3 && srcIdx+j < numSamples; j++ {
			sum += int32(samples[srcIdx+j])
			count++
		}
		avg := int16(sum / int32(count))
		output[i*2] = byte(avg)
		output[i*2+1] = byte(avg >> 8)
	}
	return output
}
