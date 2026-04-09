// ============================================================
// Pegasi Chatbot - PCS VoIP
// ============================================================

let chatbotAutoOpened = false;
let pegasiGreetingShown = false;
let currentSessionId = null;
let selectedTopic = null;
let currentUserInfo = null;
let topic_id = null;
let user_id = "";
let chat_id = 'PCSVOIP-SALES';
// Text chat uses the local Grok-backed proxy; voice chat uses same host WebSocket
var chatApiUrl = window.location.origin + '/api/chat';

let inactivityTimer = null;
let inactivityPopupTimer = null;
const INACTIVITY_POPUP_DELAY = 1 * 60 * 1000;
const INACTIVITY_CLOSE_DELAY = 2 * 60 * 1000;

// --- Pegasi greeting popup (appears 2-3s after page load) ---
document.addEventListener('DOMContentLoaded', function () {
    const icon = document.getElementById('chatbot-icon');
    if (icon) icon.classList.add('pulse');

    // Show the Pegasi greeting bubble after 2.5 seconds
    setTimeout(function () {
        if (!chatbotAutoOpened) {
            showPegasiGreeting();
        }
    }, 2500);
});

function showPegasiGreeting() {
    if (pegasiGreetingShown) return;
    pegasiGreetingShown = true;

    // Create the bubble element and append it to body
    var bubble = document.createElement('div');
    bubble.id = 'pegasi-greeting';
    bubble.innerHTML =
        '<button class="pegasi-close" onclick="closePegasiGreeting()">&times;</button>' +
        '<span class="pegasi-name">Hi, I\'m Pegasi!</span>' +
        '<button class="pegasi-mic" onclick="startVoiceSession()" title="Talk to Pegasi"><i class="fas fa-microphone"></i></button>' +
        '<br>How can I help you today?';
    document.body.appendChild(bubble);

    // Force a reflow before adding .show so the transition fires
    bubble.offsetHeight;
    bubble.classList.add('show');
}

function closePegasiGreeting() {
    var bubble = document.getElementById('pegasi-greeting');
    if (bubble) {
        bubble.classList.remove('show');
        setTimeout(function () { bubble.remove(); }, 400);
    }
}

// --- Main chatbot toggle ---
function toggleChatbot() {
    const chatbotWindow = document.getElementById('chatbot-window');
    const chatbotIcon = document.getElementById('chatbot-icon');
    const overlay = document.getElementById('chatbot-overlay');

    chatbotAutoOpened = true;
    chatbotIcon.classList.remove('pulse');
    closePegasiGreeting();

    loadTopic();

    if (chatbotWindow.style.display === 'none' || !chatbotWindow.style.display) {
        chatbotWindow.style.display = 'flex';
        overlay.style.display = 'block';
        chatbotIcon.style.width = '40px';
        chatbotIcon.style.height = '40px';
        chatbotIcon.style.fontSize = '20px';

        if (currentUserInfo) {
            // Returning user — go straight to chat
            document.getElementById('user-form').style.display = 'none';
            document.getElementById('chat-messages').style.display = 'block';
            document.getElementById('chatbot-footer').style.display = 'block';
            document.getElementById('chatbot-input').disabled = false;
        } else {
            // New visitor — show Pegasi greeting + typing area
            var chatMessages = document.getElementById('chat-messages');
            chatMessages.innerHTML =
                '<div class="message bot-message">' +
                'Hi, I\'m <strong>Pegasi</strong> — your PCS VoIP assistant! ' +
                'Type a question below, or I can connect you with our team.' +
                '</div>';
            chatMessages.style.display = 'block';

            // Hide the user-form initially; show typing input
            document.getElementById('user-form').style.display = 'none';
            document.getElementById('chatbot-footer').style.display = 'block';
            document.getElementById('chatbot-input').disabled = false;
            document.getElementById('chatbot-input').placeholder = 'Type your message...';
            document.getElementById('chatbot-input').focus();
        }
        resetInactivityTimers();
    } else {
        closeChatbot();
    }
}

// --- Handle user's first message (transition to detail collection) ---
function handleKeyPress(event) {
    if (event.key === 'Enter') {
        var input = document.getElementById('chatbot-input');
        var message = input.value.trim();
        if (!message) return;

        if (!currentUserInfo) {
            // First message from a new visitor — show it, then ask for details
            addMessage(message, true);
            input.value = '';
            input.disabled = true;
            document.getElementById('chatbot-footer').style.display = 'none';

            // Store the first message to send once we have user info
            window._pegasiFirstMessage = message;

            var chatMessages = document.getElementById('chat-messages');
            var askDetails = document.createElement('div');
            askDetails.className = 'message bot-message';
            askDetails.innerHTML =
                'Great question! So I can help you better, could you share a few details?';
            chatMessages.appendChild(askDetails);

            // Show the user-form for detail collection
            document.getElementById('user-form').style.display = 'block';
            var chatBody = document.getElementById('chatbot-body');
            chatBody.scrollTop = chatBody.scrollHeight;
        } else if (currentSessionId === null) {
            sendMessage(message, currentUserInfo);
            input.value = '';
        } else {
            sendMessage(message);
            input.value = '';
        }
        resetInactivityTimers();
    }
}

// --- Start chat after user fills in details ---
async function startChat() {
    var firstName = document.getElementById('user-first-name').value.trim();
    var lastName = document.getElementById('user-last-name').value.trim();
    var userPhone = document.getElementById('user-phone').value.trim();
    var userEmail = document.getElementById('user-email').value.trim();

    if (!firstName || !lastName || !userPhone) {
        alert('Please fill in all required fields (First Name, Last Name, and Phone Number)');
        return;
    }

    var phoneRegex = /^\d{10}$/;
    if (!phoneRegex.test(userPhone.replace(/[-\s]/g, ''))) {
        alert('Please enter a valid 10-digit phone number');
        return;
    }

    currentUserInfo = { firstName: firstName, lastName: lastName, userPhone: userPhone, userEmail: userEmail };

    document.getElementById('user-form').style.display = 'none';
    document.getElementById('chatbot-footer').style.display = 'block';
    document.getElementById('chatbot-input').disabled = false;
    document.getElementById('chatbot-input').focus();

    // Send the first message the user typed (or a default greeting)
    var firstMsg = window._pegasiFirstMessage || 'Hi';
    window._pegasiFirstMessage = null;
    sendMessage(firstMsg, currentUserInfo);
    resetInactivityTimers();
}

// --- Send message to Grok-backed API ---
async function sendMessage(message, userInfo) {
    var chatMessages = document.getElementById('chat-messages');
    var chatBody = document.getElementById('chatbot-body');

    // Show user message in chat (skip for the very first greeting)
    addMessage(message, true);
    chatBody.scrollTop = chatBody.scrollHeight;

    var typingIndicator = document.createElement('div');
    typingIndicator.className = 'typing-indicator';
    typingIndicator.innerHTML = '<span></span><span></span><span></span>';
    chatMessages.appendChild(typingIndicator);
    chatBody.scrollTop = chatBody.scrollHeight;

    var requestBody = {
        session_id: currentSessionId || '',
        message: message,
        first_name: (userInfo && userInfo.firstName) || '',
        last_name: (userInfo && userInfo.lastName) || ''
    };

    try {
        var response = await fetch(chatApiUrl, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(requestBody)
        });
        var data = await response.json();

        typingIndicator.remove();
        currentSessionId = data.session_id;
        addMessage(data.message, false);
        resetInactivityTimers();
    } catch (error) {
        typingIndicator.remove();
        console.error('Error sending message:', error);
        addMessage("Sorry, I'm having trouble connecting. Please call us at 844-PCS-VOIP for immediate help.", false);
    }
}

// --- Close chatbot ---
async function closeChatbot() {
    var chatbotWindow = document.getElementById('chatbot-window');
    var chatbotIcon = document.getElementById('chatbot-icon');
    var overlay = document.getElementById('chatbot-overlay');

    // End voice if active
    endVoiceSession();

    chatbotWindow.style.display = 'none';
    overlay.style.display = 'none';
    chatbotIcon.style.width = '60px';
    chatbotIcon.style.height = '60px';
    chatbotIcon.style.fontSize = '28px';

    resetChat();
}

function resetChat() {
    currentSessionId = null;
    selectedTopic = null;

    document.getElementById('user-form').style.display = 'none';
    document.getElementById('chat-messages').style.display = 'none';
    document.getElementById('chatbot-footer').style.display = 'none';
    document.getElementById('chat-messages').innerHTML = '';
    document.getElementById('chatbot-input').disabled = true;
    document.getElementById('chatbot-input').value = '';
}

// --- Call initiation --- direct dial
function initiateCall() {
    window.open('tel:+18447278647', '_self');
}

// --- Time slot handling ---
function createTimeSlotButtons(message) {
    if (message.includes("available slots")) {
        var parts = message.split(':\n\n');
        var header = parts[0];
        var timeSlotsPart = parts[1];

        var messageDiv = document.createElement('div');
        messageDiv.className = 'message bot-message';
        messageDiv.textContent = header + ':';

        var buttonContainer = document.createElement('div');
        buttonContainer.className = 'time-slot-buttons';

        var timeSlots = [];
        if (timeSlotsPart) {
            timeSlots = timeSlotsPart
                .split('\n')
                .map(function (s) { return s.trim(); })
                .filter(function (s) { return s && s.match(/\d+:\d+\s*[AaPpMm]{2}/); });
        }

        timeSlots.forEach(function (slot) {
            var button = document.createElement('button');
            button.className = 'time-slot-button';
            button.textContent = slot;
            button.onclick = function () { selectTimeSlot(slot, buttonContainer); };
            buttonContainer.appendChild(button);
        });

        messageDiv.appendChild(buttonContainer);
        return messageDiv;
    }
    return null;
}

function selectTimeSlot(slot, buttonContainer) {
    var buttons = buttonContainer.getElementsByClassName('time-slot-button');
    Array.from(buttons).forEach(function (button) {
        button.disabled = true;
        if (button.textContent === slot) {
            button.classList.add('selected');
        }
    });
    sendMessage(slot);
}

// --- Add message to chat ---
function addMessage(message, isUser) {
    var chatMessages = document.getElementById('chat-messages');
    var chatBody = document.getElementById('chatbot-body');

    if (!isUser) {
        var timeSlotMessage = createTimeSlotButtons(message);
        if (timeSlotMessage) {
            chatMessages.appendChild(timeSlotMessage);
            chatBody.scrollTop = chatBody.scrollHeight;
            return;
        }
    }

    var messageDiv = document.createElement('div');
    messageDiv.className = 'message ' + (isUser ? 'user-message' : 'bot-message');
    messageDiv.textContent = message;
    chatMessages.appendChild(messageDiv);
    chatBody.scrollTop = chatBody.scrollHeight;
}

// --- Load topic (no-op, context is handled by the Grok proxy) ---
function loadTopic() {}

// --- Inactivity timers ---
function resetInactivityTimers() {
    if (inactivityTimer) clearTimeout(inactivityTimer);
    if (inactivityPopupTimer) clearTimeout(inactivityPopupTimer);

    var chatbotWindow = document.getElementById('chatbot-window');
    if (currentSessionId && chatbotWindow.style.display === 'flex') {
        inactivityPopupTimer = setTimeout(showInactivityPopup, INACTIVITY_POPUP_DELAY);
        inactivityTimer = setTimeout(handleInactivityTimeout, INACTIVITY_CLOSE_DELAY);
    }
}

function handleInactivityTimeout() {
    var chatbotWindow = document.getElementById('chatbot-window');
    if (currentSessionId && chatbotWindow.style.display === 'flex') {
        closeChatbot();
    }
}

function showInactivityPopup() {
    var chatbotWindow = document.getElementById('chatbot-window');
    if (!currentSessionId || chatbotWindow.style.display !== 'flex') return;

    var chatMessages = document.getElementById('chat-messages');
    var msg = document.createElement('div');
    msg.className = 'message bot-message';
    msg.innerHTML =
        '<div style="text-align: center;">' +
        '<p>Are you still there? This chat will close automatically in 1 minute due to inactivity.</p>' +
        '<button onclick="continueChat()" style="background: #25D366; color: white; border: none; padding: 8px 16px; border-radius: 5px; margin: 6px; cursor: pointer;">Continue Chat</button>' +
        '<button onclick="closeChatbot()" style="background: #dc3545; color: white; border: none; padding: 8px 16px; border-radius: 5px; margin: 6px; cursor: pointer;">End Chat</button>' +
        '</div>';
    chatMessages.appendChild(msg);
    document.getElementById('chatbot-body').scrollTop = document.getElementById('chatbot-body').scrollHeight;
}

function continueChat() {
    var chatMessages = document.getElementById('chat-messages');
    var messages = chatMessages.getElementsByClassName('message');
    if (messages.length > 0) {
        var last = messages[messages.length - 1];
        if (last.innerHTML.includes('Are you still there?')) {
            last.remove();
        }
    }
    resetInactivityTimers();
    document.getElementById('chatbot-input').focus();
}

// --- Clean up on page unload ---
window.addEventListener('beforeunload', function () {
    var chatbotWindow = document.getElementById('chatbot-window');
    if (currentSessionId && chatbotWindow.style.display === 'flex') {
        closeChatbot();
    }
    endVoiceSession();
});

// ============================================================
// Pegasi Voice Mode — Grok Realtime Voice via WebSocket Proxy
// ============================================================

var voiceWs = null;
var voiceAudioCtx = null;
var voiceMicStream = null;
var voiceMicProcessor = null;
var voiceActive = false;
var voicePlaybackQueue = [];
var voiceIsPlaying = false;

// Determine the voice proxy WebSocket URL
function getVoiceProxyUrl() {
    var loc = window.location;
    var wsProto = loc.protocol === 'https:' ? 'wss:' : 'ws:';
    // In production (HTTPS), Nginx proxies /ws/voice to port 9081
    // In development (HTTP), connect directly to port 9081
    if (loc.protocol === 'https:') {
        return wsProto + '//' + loc.host + '/ws/voice?voice=ara';
    }
    return wsProto + '//' + loc.hostname + ':9081/ws/voice?voice=ara';
}

// Start a voice session — called when user clicks the mic icon
async function startVoiceSession() {
    // Close the greeting popup if open
    closePegasiGreeting();
    chatbotAutoOpened = true;
    var icon = document.getElementById('chatbot-icon');
    if (icon) icon.classList.remove('pulse');

    // Open the chatbot window if not already open
    var chatbotWindow = document.getElementById('chatbot-window');
    var overlay = document.getElementById('chatbot-overlay');
    if (chatbotWindow.style.display !== 'flex') {
        chatbotWindow.style.display = 'flex';
        overlay.style.display = 'block';
        if (icon) {
            icon.style.width = '40px';
            icon.style.height = '40px';
            icon.style.fontSize = '20px';
        }
    }

    // Hide text chat elements, show voice mode
    var chatBody = document.getElementById('chatbot-body');
    var chatFooter = document.getElementById('chatbot-footer');
    var voiceMode = document.getElementById('voice-mode');
    if (!voiceMode) return;

    chatBody.style.display = 'none';
    chatFooter.style.display = 'none';
    voiceMode.classList.add('active');

    updateVoiceStatus('Connecting...');
    updateVoiceTranscript('');

    try {
        // Request microphone access
        voiceMicStream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch (err) {
        updateVoiceStatus('Microphone access denied');
        console.error('Mic access error:', err);
        setTimeout(endVoiceSession, 2000);
        return;
    }

    // Create audio context at 24kHz for Grok
    voiceAudioCtx = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: 24000 });

    // Connect to voice proxy
    try {
        voiceWs = new WebSocket(getVoiceProxyUrl());
    } catch (err) {
        updateVoiceStatus('Connection failed');
        console.error('WebSocket error:', err);
        setTimeout(endVoiceSession, 2000);
        return;
    }

    voiceWs.onopen = function () {
        voiceActive = true;
        updateVoiceStatus('Listening...');
        startMicCapture();
    };

    voiceWs.onmessage = function (event) {
        try {
            var msg = JSON.parse(event.data);
            handleVoiceEvent(msg);
        } catch (e) {
            console.error('Voice message parse error:', e);
        }
    };

    voiceWs.onerror = function (err) {
        console.error('Voice WS error:', err);
        updateVoiceStatus('Connection error');
    };

    voiceWs.onclose = function () {
        if (voiceActive) {
            updateVoiceStatus('Disconnected');
            setTimeout(endVoiceSession, 1000);
        }
    };
}

// Handle events from Grok via proxy
function handleVoiceEvent(msg) {
    switch (msg.type) {
        case 'session.created':
        case 'session.updated':
        case 'conversation.created':
            updateVoiceStatus('Listening...');
            setOrbState('listening');
            break;

        case 'input_audio_buffer.speech_started':
            updateVoiceStatus('Hearing you...');
            setOrbState('listening');
            break;

        case 'input_audio_buffer.speech_stopped':
        case 'input_audio_buffer.committed':
            updateVoiceStatus('Thinking...');
            setOrbState('');
            break;

        case 'response.created':
            updateVoiceStatus('Pegasi is speaking...');
            setOrbState('speaking');
            break;

        case 'response.output_audio.delta':
            // Queue audio for playback
            if (msg.delta) {
                playAudioChunk(msg.delta);
            }
            break;

        case 'response.output_audio_transcript.delta':
            if (msg.delta) {
                appendVoiceTranscript(msg.delta);
            }
            break;

        case 'response.done':
            updateVoiceStatus('Listening...');
            setOrbState('listening');
            break;

        case 'conversation.item.input_audio_transcription.completed':
            if (msg.transcript) {
                setVoiceTranscript('You: ' + msg.transcript);
            }
            break;

        case 'error':
            console.error('Grok error:', msg);
            updateVoiceStatus('Error occurred');
            break;
    }
}

// Capture microphone audio and stream to proxy
function startMicCapture() {
    if (!voiceMicStream || !voiceAudioCtx || !voiceWs) return;

    var source = voiceAudioCtx.createMediaStreamSource(voiceMicStream);

    // Use ScriptProcessorNode (widely supported) to capture PCM
    var bufferSize = 4096;
    voiceMicProcessor = voiceAudioCtx.createScriptProcessor(bufferSize, 1, 1);

    voiceMicProcessor.onaudioprocess = function (e) {
        if (!voiceActive || !voiceWs || voiceWs.readyState !== WebSocket.OPEN) return;

        var inputData = e.inputBuffer.getChannelData(0);

        // Resample from AudioContext sampleRate to 24000 if needed
        var targetRate = 24000;
        var ratio = voiceAudioCtx.sampleRate / targetRate;
        var targetLength = Math.round(inputData.length / ratio);
        var resampled = new Float32Array(targetLength);

        for (var i = 0; i < targetLength; i++) {
            var srcIdx = Math.min(Math.round(i * ratio), inputData.length - 1);
            resampled[i] = inputData[srcIdx];
        }

        // Convert Float32 to Int16 PCM
        var pcm16 = new Int16Array(resampled.length);
        for (var j = 0; j < resampled.length; j++) {
            var s = Math.max(-1, Math.min(1, resampled[j]));
            pcm16[j] = s < 0 ? s * 0x8000 : s * 0x7FFF;
        }

        // Base64 encode
        var bytes = new Uint8Array(pcm16.buffer);
        var binary = '';
        for (var k = 0; k < bytes.length; k++) {
            binary += String.fromCharCode(bytes[k]);
        }
        var b64 = btoa(binary);

        voiceWs.send(JSON.stringify({
            type: 'input_audio_buffer.append',
            audio: b64
        }));
    };

    source.connect(voiceMicProcessor);
    voiceMicProcessor.connect(voiceAudioCtx.destination);
}

// Play received audio (base64 PCM16 at 24kHz)
function playAudioChunk(b64Data) {
    if (!voiceAudioCtx) return;

    var binary = atob(b64Data);
    var bytes = new Uint8Array(binary.length);
    for (var i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i);
    }

    var pcm16 = new Int16Array(bytes.buffer);
    var float32 = new Float32Array(pcm16.length);
    for (var j = 0; j < pcm16.length; j++) {
        float32[j] = pcm16[j] / 32768.0;
    }

    var audioBuffer = voiceAudioCtx.createBuffer(1, float32.length, 24000);
    audioBuffer.getChannelData(0).set(float32);

    voicePlaybackQueue.push(audioBuffer);
    drainPlaybackQueue();
}

var nextPlayTime = 0;

function drainPlaybackQueue() {
    if (voiceIsPlaying || voicePlaybackQueue.length === 0 || !voiceAudioCtx) return;
    voiceIsPlaying = true;

    var buffer = voicePlaybackQueue.shift();
    var source = voiceAudioCtx.createBufferSource();
    source.buffer = buffer;
    source.connect(voiceAudioCtx.destination);

    var now = voiceAudioCtx.currentTime;
    var startTime = Math.max(now, nextPlayTime);
    source.start(startTime);
    nextPlayTime = startTime + buffer.duration;

    source.onended = function () {
        voiceIsPlaying = false;
        drainPlaybackQueue();
    };
}

// End voice session
function endVoiceSession() {
    voiceActive = false;

    if (voiceMicProcessor) {
        voiceMicProcessor.disconnect();
        voiceMicProcessor = null;
    }
    if (voiceMicStream) {
        voiceMicStream.getTracks().forEach(function (t) { t.stop(); });
        voiceMicStream = null;
    }
    if (voiceWs) {
        voiceWs.close();
        voiceWs = null;
    }
    if (voiceAudioCtx) {
        voiceAudioCtx.close().catch(function () {});
        voiceAudioCtx = null;
    }
    voicePlaybackQueue = [];
    voiceIsPlaying = false;
    nextPlayTime = 0;

    // Hide voice mode overlay
    var voiceMode = document.getElementById('voice-mode');
    if (voiceMode) voiceMode.classList.remove('active');

    // Only restore text chat if the chatbot window is still open
    var chatbotWindow = document.getElementById('chatbot-window');
    if (chatbotWindow && chatbotWindow.style.display === 'flex') {
        var chatBody = document.getElementById('chatbot-body');
        var chatFooter = document.getElementById('chatbot-footer');
        var chatMessages = document.getElementById('chat-messages');
        var chatInput = document.getElementById('chatbot-input');

        // Make chat body and footer visible
        if (chatBody) chatBody.style.display = 'block';
        if (chatFooter) chatFooter.style.display = 'block';

        // Show chat messages area
        if (chatMessages) chatMessages.style.display = 'block';

        // Enable the text input so user can type
        if (chatInput) {
            chatInput.disabled = false;
            chatInput.placeholder = 'Type your message...';
            chatInput.focus();
        }

        // If no messages exist yet, show a welcome back message
        if (chatMessages && chatMessages.children.length === 0) {
            chatMessages.innerHTML =
                '<div class="message bot-message">' +
                'Hi, I\'m <strong>Pegasi</strong>! Type your question below or click the mic to talk.' +
                '</div>';
        }
    }
}

// UI helpers
function updateVoiceStatus(text) {
    var el = document.getElementById('voice-status');
    if (el) el.textContent = text;
}

function updateVoiceTranscript(text) {
    var el = document.getElementById('voice-transcript');
    if (el) el.textContent = text;
}

function setVoiceTranscript(text) {
    var el = document.getElementById('voice-transcript');
    if (el) el.textContent = text;
}

function appendVoiceTranscript(text) {
    var el = document.getElementById('voice-transcript');
    if (el) {
        // If starts with "You:", add a new line for Pegasi
        if (el.textContent.startsWith('You:') && !el.textContent.includes('\nPegasi: ')) {
            el.textContent += '\nPegasi: ';
        } else if (el.textContent === '') {
            el.textContent = 'Pegasi: ';
        }
        el.textContent += text;
        el.scrollTop = el.scrollHeight;
    }
}

function setOrbState(state) {
    var orb = document.querySelector('#voice-mode .voice-orb');
    if (!orb) return;
    orb.classList.remove('listening', 'speaking');
    if (state) orb.classList.add(state);
}
