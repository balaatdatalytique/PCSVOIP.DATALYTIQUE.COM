package main

// crm.go — CRM integration for booking appointments via crm.pegasiai.com API.

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
)

// ===================== CRM CLIENT =====================

var (
	crmClient   *crmAPI
	crmInitOnce sync.Once
)

type crmAPI struct {
	baseURL  string
	username string
	password string
	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

func getCRM() *crmAPI {
	crmInitOnce.Do(func() {
		crmClient = &crmAPI{
			baseURL:  envOr("CRM_API_URL", "http://smartcrm:9005"),
			username: envOr("CRM_USERNAME", "admin@pegasiai.com"),
			password: envOr("CRM_PASSWORD", "Admin@2026"),
		}
	})
	return crmClient
}

// authenticate obtains a bearer token from the CRM login endpoint.
func (c *crmAPI) authenticate() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Reuse valid token
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return nil
	}

	body, _ := json.Marshal(map[string]string{
		"username": c.username,
		"password": c.password,
	})
	resp, err := http.Post(c.baseURL+"/api/v1/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("crm login: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return fmt.Errorf("crm login status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		TokenID string `json:"token_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("crm login parse: %w", err)
	}
	if result.TokenID == "" {
		// Try alternate field names
		var alt map[string]interface{}
		json.Unmarshal(respBody, &alt)
		if t, ok := alt["token"].(string); ok {
			result.TokenID = t
		} else if t, ok := alt["session_id"].(string); ok {
			result.TokenID = t
		}
	}
	if result.TokenID == "" {
		return fmt.Errorf("crm login: no token in response: %s", string(respBody))
	}

	c.token = result.TokenID
	c.tokenExp = time.Now().Add(23 * time.Hour) // refresh before 24h TTL
	log.Printf("crm: authenticated as %s", c.username)
	return nil
}

// CreateAppointment books an appointment in the CRM.
func (c *crmAPI) CreateAppointment(contactName, phone, date, startTime, endTime, notes string) (string, error) {
	if err := c.authenticate(); err != nil {
		return "", err
	}

	appt := map[string]string{
		"contact_name":  contactName,
		"phone_number":  phone,
		"date":          date,
		"notes":         notes,
		"status":        "scheduled",
		"campaign_name": "Website AI Callback",
	}
	if startTime != "" {
		appt["start_time"] = startTime
	}
	if endTime != "" {
		appt["end_time"] = endTime
	}

	body, _ := json.Marshal(appt)
	req, _ := http.NewRequest("POST", c.baseURL+"/api/v1/appointments", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("crm create appointment: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return "", fmt.Errorf("crm appointment status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	json.Unmarshal(respBody, &result)

	log.Printf("crm: appointment created for %s on %s (id=%s)", contactName, date, result.ID)
	return result.ID, nil
}

// ===================== GROK TOOL DEFINITION =====================

var bookAppointmentToolDef = map[string]interface{}{
	"type": "function",
	"function": map[string]interface{}{
		"name":        "book_appointment",
		"description": "Book a follow-up appointment or demo for the caller in the PCS VoIP CRM. Use this when the caller wants to schedule a meeting, demo, consultation, or callback. Ask the caller for their preferred date and time before calling this tool. Confirm the booking details with the caller after the appointment is created.",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"date": map[string]interface{}{
					"type":        "string",
					"description": "Appointment date in YYYY-MM-DD format. Convert natural language like 'next Tuesday' or 'tomorrow' to the actual date.",
				},
				"start_time": map[string]interface{}{
					"type":        "string",
					"description": "Start time in HH:mm (24-hour) format. Convert '2pm' to '14:00', '10:30am' to '10:30', etc.",
				},
				"end_time": map[string]interface{}{
					"type":        "string",
					"description": "End time in HH:mm (24-hour) format. If not specified, default to 30 minutes after start_time.",
				},
				"notes": map[string]interface{}{
					"type":        "string",
					"description": "Brief notes about what the appointment is for (e.g., 'VoIP demo for 25 phones', 'SIP trunking consultation').",
				},
			},
			"required": []string{"date", "start_time"},
		},
	},
}

// handleBookAppointment executes the book_appointment tool call.
func handleBookAppointment(callerName, callerPhone, argsJSON string) string {
	var args struct {
		Date      string `json:"date"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		Notes     string `json:"notes"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("Failed to parse appointment details: %v", err)
	}

	if args.Date == "" || args.StartTime == "" {
		return "Missing required fields: date and start_time are required."
	}

	// Default end time to 30 min after start
	if args.EndTime == "" {
		if t, err := time.Parse("15:04", args.StartTime); err == nil {
			args.EndTime = t.Add(30 * time.Minute).Format("15:04")
		}
	}

	// Format phone for CRM
	phone := callerPhone
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}

	crm := getCRM()
	id, err := crm.CreateAppointment(callerName, phone, args.Date, args.StartTime, args.EndTime, args.Notes)
	if err != nil {
		log.Printf("crm: appointment error: %v", err)
		return fmt.Sprintf("I wasn't able to book the appointment in our system right now. Please note: %s requested an appointment on %s at %s. Our team will confirm this manually.", callerName, args.Date, args.StartTime)
	}

	return fmt.Sprintf("Appointment successfully booked! Details: %s on %s from %s to %s. Appointment ID: %s. Please confirm these details with the caller.", callerName, args.Date, args.StartTime, args.EndTime, id)
}

// ===================== ENV HELPER =====================

func envOrCRM(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
