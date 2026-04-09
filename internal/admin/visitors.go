package admin

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"pcsvoip-cms/internal/db"
)

// VisitorRepo persists visitor summaries and the chronological event log.
type VisitorRepo struct {
	DB *db.DB
	mu sync.Mutex // serialises read-modify-write of Visitor summaries
}

func NewVisitorRepo(database *db.DB) *VisitorRepo { return &VisitorRepo{DB: database} }

const (
	maxMessageLen = 500
)

// Track records one event and updates the per-visitor summary.
func (r *VisitorRepo) Track(ev VisitorEvent) error {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if ev.ID == "" {
		ev.ID = NewID()
	}
	if ev.VisitorID == "" {
		// derive from IP+UA fallback (shouldn't happen, but safe)
		ev.VisitorID = VisitorID(ev.IP, "")
	}
	if len(ev.Message) > maxMessageLen {
		ev.Message = ev.Message[:maxMessageLen] + "…"
	}
	if len(ev.Reply) > maxMessageLen {
		ev.Reply = ev.Reply[:maxMessageLen] + "…"
	}
	if err := r.DB.Put(db.BucketEvents, ev.ID, ev); err != nil {
		return err
	}
	return r.upsertVisitor(ev)
}

func (r *VisitorRepo) upsertVisitor(ev VisitorEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var v Visitor
	err := r.DB.Get(db.BucketVisitors, ev.VisitorID, &v)
	if errors.Is(err, db.ErrNotFound) {
		v = Visitor{
			ID:        ev.VisitorID,
			FirstSeen: ev.Timestamp,
		}
	} else if err != nil {
		return err
	}
	v.LastSeen = ev.Timestamp
	v.LastIP = ev.IP
	if ev.Type == "page_view" {
		v.PageViews++
	} else if ev.Type == "bot_chat" || ev.Type == "bot_voice" {
		v.BotMsgs++
	}
	return r.DB.Put(db.BucketVisitors, v.ID, v)
}

// RecentEvents returns up to limit events, newest first.
func (r *VisitorRepo) RecentEvents(limit int) ([]VisitorEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	out := make([]VisitorEvent, 0, limit)
	err := r.DB.ForEachReverse(db.BucketEvents, limit, func(_ string, raw []byte) error {
		var ev VisitorEvent
		if e := json.Unmarshal(raw, &ev); e != nil {
			return nil
		}
		out = append(out, ev)
		return nil
	})
	return out, err
}

// CountEventsSince returns the number of events with timestamp >= since.
func (r *VisitorRepo) CountEventsSince(since time.Time) (int, error) {
	n := 0
	err := r.DB.ForEachReverse(db.BucketEvents, 0, func(_ string, raw []byte) error {
		var ev VisitorEvent
		if e := json.Unmarshal(raw, &ev); e != nil {
			return nil
		}
		if ev.Timestamp.Before(since) {
			return errStopIter
		}
		n++
		return nil
	})
	if err != nil && !errors.Is(err, errStopIter) {
		return n, err
	}
	return n, nil
}

// ListVisitors returns a sorted list of visitor summaries (most recent first).
func (r *VisitorRepo) ListVisitors(limit int) ([]Visitor, error) {
	visitors := make([]Visitor, 0)
	err := r.DB.ForEach(db.BucketVisitors, func(_ string, raw []byte) error {
		var v Visitor
		if e := json.Unmarshal(raw, &v); e != nil {
			return nil
		}
		visitors = append(visitors, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// sort by LastSeen desc
	for i := 1; i < len(visitors); i++ {
		for j := i; j > 0 && visitors[j].LastSeen.After(visitors[j-1].LastSeen); j-- {
			visitors[j], visitors[j-1] = visitors[j-1], visitors[j]
		}
	}
	if limit > 0 && len(visitors) > limit {
		visitors = visitors[:limit]
	}
	return visitors, nil
}

// CountVisitors returns the number of unique visitors.
func (r *VisitorRepo) CountVisitors() (int, error) {
	return r.DB.Count(db.BucketVisitors)
}

// FormatType returns a human-friendly label for an event type.
func FormatType(t string) string {
	switch t {
	case "page_view":
		return "Page view"
	case "bot_chat":
		return "Chat"
	case "bot_voice":
		return "Voice"
	}
	return strings.Title(t)
}

// errStopIter is a sentinel used to short-circuit reverse iteration.
var errStopIter = errors.New("stop")
