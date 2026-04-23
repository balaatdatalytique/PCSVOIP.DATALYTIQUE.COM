package admin

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"pcsvoip-cms/internal/db"
)

// VisitorRepo persists visitor summaries and the chronological event log,
// and triggers async geolocation lookups for new visitors.
type VisitorRepo struct {
	DB  *db.DB
	Geo *GeoService
	mu  sync.Mutex // serialises read-modify-write of Visitor summaries
}

func NewVisitorRepo(database *db.DB, geo *GeoService) *VisitorRepo {
	return &VisitorRepo{DB: database, Geo: geo}
}

const (
	maxMessageLen      = 500
	defaultEventsLimit = 50
)

// Track records one event and updates the per-visitor summary. On the visitor's
// first sighting (or whenever geo data is missing/stale) it kicks off an async
// geo lookup; the resulting fields are written back to the visitor record.
func (r *VisitorRepo) Track(ev VisitorEvent) error {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if ev.ID == "" {
		ev.ID = NewID()
	}
	if ev.VisitorID == "" {
		ev.VisitorID = VisitorID(ev.IP, ev.UserAgent)
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
	if err := r.upsertVisitor(ev); err != nil {
		return err
	}
	if r.Geo != nil && ev.IP != "" {
		visitorID := ev.VisitorID
		r.Geo.Enrich(ev.IP, func(info *GeoInfo) {
			r.applyGeo(visitorID, info)
		})
	}
	return nil
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
	if ev.UserAgent != "" {
		v.UserAgent = ev.UserAgent
	}
	switch ev.Type {
	case "page_view":
		v.PageViews++
	case "bot_chat", "bot_voice":
		v.BotMsgs++
	}
	return r.DB.Put(db.BucketVisitors, v.ID, v)
}

// applyGeo merges a freshly-resolved GeoInfo into the visitor record.
func (r *VisitorRepo) applyGeo(visitorID string, info *GeoInfo) {
	if info == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var v Visitor
	if err := r.DB.Get(db.BucketVisitors, visitorID, &v); err != nil {
		return
	}
	v.City = info.City
	v.Region = info.Region
	v.Country = info.Country
	v.CountryCode = info.CountryCode
	v.ISP = info.ISP
	v.Latitude = info.Latitude
	v.Longitude = info.Longitude
	_ = r.DB.Put(db.BucketVisitors, v.ID, v)
}

// EventsPage carries one page of events plus pagination metadata.
type EventsPage struct {
	Events   []VisitorEvent
	Page     int
	PerPage  int
	Total    int
	HasPrev  bool
	HasNext  bool
	PrevPage int
	NextPage int
	LastPage int
}

// VisitorsPage carries one page of visitors plus pagination metadata.
type VisitorsPage struct {
	Visitors []Visitor
	Page     int
	PerPage  int
	Total    int
	HasPrev  bool
	HasNext  bool
	PrevPage int
	NextPage int
	LastPage int
}

// RecentEvents returns up to limit events, newest first. Kept for the
// dashboard "recent activity" widget where pagination isn't needed.
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

// RecentEventsPaged returns one page of events sorted newest first. perPage is
// clamped to 1..200; page is clamped to 1..LastPage.
func (r *VisitorRepo) RecentEventsPaged(page, perPage int) (EventsPage, error) {
	if perPage <= 0 {
		perPage = defaultEventsLimit
	}
	if perPage > 200 {
		perPage = 200
	}
	if page < 1 {
		page = 1
	}
	total, err := r.DB.Count(db.BucketEvents)
	if err != nil {
		return EventsPage{}, err
	}
	last := (total + perPage - 1) / perPage
	if last < 1 {
		last = 1
	}
	if page > last {
		page = last
	}
	offset := (page - 1) * perPage

	events := make([]VisitorEvent, 0, perPage)
	skipped := 0
	err = r.DB.ForEachReverse(db.BucketEvents, 0, func(_ string, raw []byte) error {
		if skipped < offset {
			skipped++
			return nil
		}
		if len(events) >= perPage {
			return errStopIter
		}
		var ev VisitorEvent
		if e := json.Unmarshal(raw, &ev); e != nil {
			return nil
		}
		events = append(events, ev)
		return nil
	})
	if err != nil && !errors.Is(err, errStopIter) {
		return EventsPage{}, err
	}
	return EventsPage{
		Events:   events,
		Page:     page,
		PerPage:  perPage,
		Total:    total,
		HasPrev:  page > 1,
		HasNext:  page < last,
		PrevPage: page - 1,
		NextPage: page + 1,
		LastPage: last,
	}, nil
}

// CountByTypeSince returns (pageViews, chats, voices) for events newer than since.
// Uses one reverse pass with early-exit when timestamps drop below the window.
func (r *VisitorRepo) CountByTypeSince(since time.Time) (int, int, int) {
	var pv, ch, vo int
	_ = r.DB.ForEachReverse(db.BucketEvents, 0, func(_ string, raw []byte) error {
		var ev VisitorEvent
		if e := json.Unmarshal(raw, &ev); e != nil {
			return nil
		}
		if ev.Timestamp.Before(since) {
			return errStopIter
		}
		switch ev.Type {
		case "page_view":
			pv++
		case "bot_chat":
			ch++
		case "bot_voice":
			vo++
		}
		return nil
	})
	return pv, ch, vo
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

// ListVisitors returns all visitors sorted by LastSeen desc, optionally capped.
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
	sort.Slice(visitors, func(i, j int) bool {
		return visitors[i].LastSeen.After(visitors[j].LastSeen)
	})
	if limit > 0 && len(visitors) > limit {
		visitors = visitors[:limit]
	}
	return visitors, nil
}

// ListVisitorsPaged returns one page of visitors sorted by LastSeen desc.
func (r *VisitorRepo) ListVisitorsPaged(page, perPage int) (VisitorsPage, error) {
	if perPage <= 0 {
		perPage = defaultEventsLimit
	}
	if perPage > 200 {
		perPage = 200
	}
	if page < 1 {
		page = 1
	}
	all, err := r.ListVisitors(0)
	if err != nil {
		return VisitorsPage{}, err
	}
	total := len(all)
	last := (total + perPage - 1) / perPage
	if last < 1 {
		last = 1
	}
	if page > last {
		page = last
	}
	offset := (page - 1) * perPage
	end := offset + perPage
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}
	return VisitorsPage{
		Visitors: all[offset:end],
		Page:     page,
		PerPage:  perPage,
		Total:    total,
		HasPrev:  page > 1,
		HasNext:  page < last,
		PrevPage: page - 1,
		NextPage: page + 1,
		LastPage: last,
	}, nil
}

// CountVisitors returns the number of unique visitors.
func (r *VisitorRepo) CountVisitors() (int, error) {
	return r.DB.Count(db.BucketVisitors)
}

// PageVisit is one page view in a visitor's chronological history. DwellSeconds
// is the gap between this page and the next page view from the same visitor;
// the last page in a session has DwellSeconds = 0 (unknown).
type PageVisit struct {
	Path         string
	Timestamp    time.Time
	Referrer     string
	DwellSeconds int
}

// PageStat is a single row in a visitor's "where did they spend their time"
// breakdown — one entry per unique path.
type PageStat struct {
	Path        string
	Visits      int
	TotalDwell  int // seconds, summed over all visits to this path
}

// VisitorPageSummary is the compact per-visitor rollup shown in the visitors
// table. Computed in batch by PageStatsForVisitors.
type VisitorPageSummary struct {
	TopPath        string // path with the highest visit count
	TopPathVisits  int
	TotalDwellSecs int // sum across the whole session
	UniquePaths    int
}

// VisitorDetail is everything needed for the per-visitor drill-down page.
type VisitorDetail struct {
	Visitor       Visitor
	Pages         []PageVisit  // chronological
	PageStats     []PageStat   // sorted by Visits desc
	BotEvents     []VisitorEvent // chat + voice
	TotalDwellSec int
	UniquePaths   int
}

// PageStatsForVisitors does ONE pass over the events bucket and returns a
// summary keyed by visitor id. Used by the visitors table to enrich each row
// without N+1 queries.
func (r *VisitorRepo) PageStatsForVisitors(visitorIDs []string) (map[string]VisitorPageSummary, error) {
	if len(visitorIDs) == 0 {
		return map[string]VisitorPageSummary{}, nil
	}
	target := make(map[string]bool, len(visitorIDs))
	for _, id := range visitorIDs {
		target[id] = true
	}
	per := make(map[string][]VisitorEvent, len(visitorIDs))
	err := r.DB.ForEach(db.BucketEvents, func(_ string, raw []byte) error {
		var ev VisitorEvent
		if e := json.Unmarshal(raw, &ev); e != nil {
			return nil
		}
		if !target[ev.VisitorID] || ev.Type != "page_view" {
			return nil
		}
		per[ev.VisitorID] = append(per[ev.VisitorID], ev)
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]VisitorPageSummary, len(per))
	for id, evs := range per {
		out[id] = computeSummary(evs)
	}
	return out, nil
}

// computeSummary builds a VisitorPageSummary from a slice of page_view events
// for a single visitor. Sorts by time, derives top path + dwell.
func computeSummary(evs []VisitorEvent) VisitorPageSummary {
	if len(evs) == 0 {
		return VisitorPageSummary{}
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].Timestamp.Before(evs[j].Timestamp) })
	counts := make(map[string]int, len(evs))
	for _, e := range evs {
		counts[e.Path]++
	}
	topPath := ""
	topVisits := 0
	for p, n := range counts {
		if n > topVisits {
			topPath, topVisits = p, n
		}
	}
	total := 0
	for i := 0; i < len(evs)-1; i++ {
		gap := int(evs[i+1].Timestamp.Sub(evs[i].Timestamp).Seconds())
		// Cap a single dwell at 30 minutes — anything longer is almost
		// certainly a closed tab and shouldn't pollute the average.
		if gap < 0 {
			gap = 0
		}
		if gap > 1800 {
			gap = 1800
		}
		total += gap
	}
	return VisitorPageSummary{
		TopPath:        topPath,
		TopPathVisits:  topVisits,
		TotalDwellSecs: total,
		UniquePaths:    len(counts),
	}
}

// GetVisitorDetail returns everything needed for the per-visitor drill-down
// page: the Visitor record, all page visits with dwell times, per-path stats,
// and the visitor's bot interactions.
// GetVisitor returns the Visitor summary record by ID.
func (r *VisitorRepo) GetVisitor(visitorID string) (*Visitor, error) {
	var v Visitor
	if err := r.DB.Get(db.BucketVisitors, visitorID, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *VisitorRepo) GetVisitorDetail(visitorID string) (*VisitorDetail, error) {
	var v Visitor
	if err := r.DB.Get(db.BucketVisitors, visitorID, &v); err != nil {
		return nil, err
	}

	var pageEvents []VisitorEvent
	var botEvents []VisitorEvent
	err := r.DB.ForEach(db.BucketEvents, func(_ string, raw []byte) error {
		var ev VisitorEvent
		if e := json.Unmarshal(raw, &ev); e != nil {
			return nil
		}
		if ev.VisitorID != visitorID {
			return nil
		}
		switch ev.Type {
		case "page_view":
			pageEvents = append(pageEvents, ev)
		case "bot_chat", "bot_voice":
			botEvents = append(botEvents, ev)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(pageEvents, func(i, j int) bool { return pageEvents[i].Timestamp.Before(pageEvents[j].Timestamp) })
	sort.Slice(botEvents, func(i, j int) bool { return botEvents[i].Timestamp.After(botEvents[j].Timestamp) })

	pages := make([]PageVisit, 0, len(pageEvents))
	pathStats := map[string]*PageStat{}
	totalDwell := 0
	for i, e := range pageEvents {
		dwell := 0
		if i+1 < len(pageEvents) {
			gap := int(pageEvents[i+1].Timestamp.Sub(e.Timestamp).Seconds())
			if gap < 0 {
				gap = 0
			}
			if gap > 1800 {
				gap = 1800
			}
			dwell = gap
			totalDwell += dwell
		}
		pages = append(pages, PageVisit{
			Path:         e.Path,
			Timestamp:    e.Timestamp,
			Referrer:     e.Referrer,
			DwellSeconds: dwell,
		})
		ps, ok := pathStats[e.Path]
		if !ok {
			ps = &PageStat{Path: e.Path}
			pathStats[e.Path] = ps
		}
		ps.Visits++
		ps.TotalDwell += dwell
	}
	stats := make([]PageStat, 0, len(pathStats))
	for _, ps := range pathStats {
		stats = append(stats, *ps)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Visits != stats[j].Visits {
			return stats[i].Visits > stats[j].Visits
		}
		return stats[i].TotalDwell > stats[j].TotalDwell
	})

	// Show pages newest first in the chronological table.
	for i, j := 0, len(pages)-1; i < j; i, j = i+1, j-1 {
		pages[i], pages[j] = pages[j], pages[i]
	}

	return &VisitorDetail{
		Visitor:       v,
		Pages:         pages,
		PageStats:     stats,
		BotEvents:     botEvents,
		TotalDwellSec: totalDwell,
		UniquePaths:   len(pathStats),
	}, nil
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
