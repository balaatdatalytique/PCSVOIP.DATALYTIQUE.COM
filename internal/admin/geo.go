package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"pcsvoip-cms/internal/db"
)

const (
	geoTTL          = 7 * 24 * time.Hour
	geoMaxInflight  = 5
	geoLookupBudget = 5 * time.Second
)

// GeoService caches and refreshes IP geolocation data. Lookups call
// https://ipapi.co/{ip}/json/ — no API key, 1000 requests/day free, commercial
// use allowed with attribution. Results are cached in BucketGeoCache for 7
// days. Concurrency is bounded so a flood of unique visitors can't exhaust
// the upstream rate limit; excess lookups are silently dropped.
type GeoService struct {
	DB    *db.DB
	httpc *http.Client
	sem   chan struct{}
}

func NewGeoService(database *db.DB) *GeoService {
	return &GeoService{
		DB:    database,
		httpc: &http.Client{Timeout: geoLookupBudget},
		sem:   make(chan struct{}, geoMaxInflight),
	}
}

// Get returns a cached entry or nil if missing/expired.
func (g *GeoService) Get(ip string) *GeoInfo {
	if g == nil || g.DB == nil || ip == "" {
		return nil
	}
	var info GeoInfo
	if err := g.DB.Get(db.BucketGeoCache, ip, &info); err != nil {
		return nil
	}
	if time.Since(info.FetchedAt) > geoTTL {
		return nil
	}
	return &info
}

// Enrich kicks off an async lookup for ip if no fresh entry is cached. The
// optional onResolve callback fires once the result is known (cached or
// freshly fetched). Private/loopback addresses are ignored.
func (g *GeoService) Enrich(ip string, onResolve func(*GeoInfo)) {
	if g == nil || ip == "" || !isPublicIPLite(ip) {
		return
	}
	if cached := g.Get(ip); cached != nil {
		if onResolve != nil {
			onResolve(cached)
		}
		return
	}
	go g.fetch(ip, onResolve)
}

func (g *GeoService) fetch(ip string, onResolve func(*GeoInfo)) {
	// Bound concurrency. Drop the request if the queue is full — better to
	// leave the visitor without geo data than to back up indefinitely.
	select {
	case g.sem <- struct{}{}:
	default:
		return
	}
	defer func() { <-g.sem }()

	url := fmt.Sprintf("https://ipapi.co/%s/json/", ip)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "PegasiAdmin/1.0 (+https://pcsvoip.datalytique.com)")
	req.Header.Set("Accept", "application/json")

	resp, err := g.httpc.Do(req)
	if err != nil {
		log.Printf("geo: fetch %s: %v", ip, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("geo: fetch %s: HTTP %d", ip, resp.StatusCode)
		return
	}

	var raw struct {
		IP          string  `json:"ip"`
		City        string  `json:"city"`
		Region      string  `json:"region"`
		Country     string  `json:"country"`      // 2-letter ISO code
		CountryName string  `json:"country_name"` // full name
		Org         string  `json:"org"`
		ASN         string  `json:"asn"`
		Timezone    string  `json:"timezone"`
		Latitude    float64 `json:"latitude"`
		Longitude   float64 `json:"longitude"`
		Error       bool    `json:"error"`
		Reason      string  `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		log.Printf("geo: decode %s: %v", ip, err)
		return
	}
	if raw.Error {
		log.Printf("geo: ipapi.co said %s: %s", ip, raw.Reason)
		return
	}

	info := GeoInfo{
		IP:          ip,
		City:        raw.City,
		Region:      raw.Region,
		Country:     raw.CountryName,
		CountryCode: strings.ToUpper(raw.Country),
		ISP:         raw.Org,
		Latitude:    raw.Latitude,
		Longitude:   raw.Longitude,
		Timezone:    raw.Timezone,
		FetchedAt:   time.Now().UTC(),
	}
	if err := g.DB.Put(db.BucketGeoCache, ip, info); err != nil {
		log.Printf("geo: cache %s: %v", ip, err)
	}
	if onResolve != nil {
		onResolve(&info)
	}
}

// Lookup is a synchronous helper used in tests / one-shot scripts. Returns
// ErrNotFound if no fresh cached entry exists.
func (g *GeoService) Lookup(ip string) (*GeoInfo, error) {
	if info := g.Get(ip); info != nil {
		return info, nil
	}
	return nil, errors.New("geo: no cached entry")
}

// FlagEmoji returns the regional-indicator flag for an ISO 3166-1 alpha-2
// country code. Empty/invalid input returns "".
func FlagEmoji(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if len(cc) != 2 {
		return ""
	}
	if cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
		return ""
	}
	return string([]rune{
		0x1F1E6 + rune(cc[0]-'A'),
		0x1F1E6 + rune(cc[1]-'A'),
	})
}

// isPublicIPLite is a copy of middleware.IsPublicIP, kept here to avoid an
// import cycle (admin → middleware → admin via callbacks).
func isPublicIPLite(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsLinkLocalUnicast() || parsed.IsUnspecified() {
		return false
	}
	return true
}
