package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ===================== CoreDial Knowledge Base Search =====================
//
// Fetches the article index from https://docs.coredial.com/en, caches it,
// and provides keyword-based search + article content retrieval.

const (
	coreDialBaseURL  = "https://docs.coredial.com"
	coreDialIndexURL = coreDialBaseURL + "/en"
	indexCacheTTL    = 30 * time.Minute
	maxSearchResults = 3
)

// cdArticle is a single knowledge-base article from CoreDial docs.
type cdArticle struct {
	ID      string
	Title   string
	Slug    string
	Summary string
}

// articleIndex is the cached list of all articles.
var articleIndex struct {
	mu        sync.Mutex
	articles  []cdArticle
	fetchedAt time.Time
}

// fetchArticleIndex retrieves and caches the article list from the CoreDial
// docs homepage. The page embeds a window.initialData JSON blob containing
// the full category tree with all article metadata.
func fetchArticleIndex() ([]cdArticle, error) {
	articleIndex.mu.Lock()
	if len(articleIndex.articles) > 0 && time.Since(articleIndex.fetchedAt) < indexCacheTTL {
		out := articleIndex.articles
		articleIndex.mu.Unlock()
		return out, nil
	}
	articleIndex.mu.Unlock()

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(coreDialIndexURL)
	if err != nil {
		return nil, fmt.Errorf("fetch CoreDial index: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read CoreDial index: %w", err)
	}
	html := string(body)

	articles := parseArticlesFromHTML(html)
	if len(articles) == 0 {
		return nil, fmt.Errorf("no articles found in CoreDial index")
	}

	articleIndex.mu.Lock()
	articleIndex.articles = articles
	articleIndex.fetchedAt = time.Now()
	articleIndex.mu.Unlock()

	log.Printf("coredial: indexed %d articles", len(articles))
	return articles, nil
}

// parseArticlesFromHTML extracts articles from the inline initialData JSON
// embedded in the CoreDial docs HTML. The JSON is deeply nested in category
// trees, so we use regex extraction for robustness against structure changes.
func parseArticlesFromHTML(html string) []cdArticle {
	// Match article objects: {"id":NNN,"title":"...","slug":"...","summary":"..."}
	// The initialData contains articles with these fields.
	slugRe := regexp.MustCompile(`"slug"\s*:\s*"([^"]+)"`)
	titleRe := regexp.MustCompile(`"title"\s*:\s*"([^"]*)"`)
	summaryRe := regexp.MustCompile(`"summary"\s*:\s*"([^"]*)"`)
	idRe := regexp.MustCompile(`"id"\s*:\s*(\d+)`)

	// Split by article-like objects. Each article in the tree has "slug" which
	// contains {id}-{url-friendly-title}.
	slugMatches := slugRe.FindAllStringSubmatchIndex(html, -1)

	var articles []cdArticle
	seen := make(map[string]bool)

	for _, idx := range slugMatches {
		slugVal := html[idx[2]:idx[3]]
		// Article slugs look like "3-getting-support-support-policy"
		if !strings.Contains(slugVal, "-") {
			continue
		}
		if seen[slugVal] {
			continue
		}
		seen[slugVal] = true

		// Look in a window around this slug for the title, summary, id
		start := idx[0] - 500
		if start < 0 {
			start = 0
		}
		end := idx[1] + 500
		if end > len(html) {
			end = len(html)
		}
		window := html[start:end]

		var title, summary, id string
		if m := titleRe.FindStringSubmatch(window); m != nil {
			title = m[1]
		}
		if m := summaryRe.FindStringSubmatch(window); m != nil {
			summary = m[1]
		}
		if m := idRe.FindStringSubmatch(window); m != nil {
			id = m[1]
		}

		if title == "" {
			continue
		}

		articles = append(articles, cdArticle{
			ID:      id,
			Title:   unescapeJSON(title),
			Slug:    slugVal,
			Summary: unescapeJSON(summary),
		})
	}

	return articles
}

// unescapeJSON handles basic JSON string escapes.
func unescapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\t`, "\t")
	s = strings.ReplaceAll(s, `\u0026`, "&")
	s = strings.ReplaceAll(s, `\u003c`, "<")
	s = strings.ReplaceAll(s, `\u003e`, ">")
	return s
}

// searchCoreDial performs a keyword search against the CoreDial KB and
// returns the content of the top matching articles (up to maxSearchResults).
func searchCoreDial(query string) (string, error) {
	articles, err := fetchArticleIndex()
	if err != nil {
		return "", err
	}

	// Score each article by keyword match
	queryLower := strings.ToLower(query)
	words := strings.Fields(queryLower)

	type scored struct {
		article cdArticle
		score   int
	}
	var results []scored

	for _, a := range articles {
		titleLower := strings.ToLower(a.Title)
		summaryLower := strings.ToLower(a.Summary)
		score := 0

		for _, w := range words {
			if len(w) < 2 {
				continue
			}
			// Title matches worth more
			if strings.Contains(titleLower, w) {
				score += 10
			}
			if strings.Contains(summaryLower, w) {
				score += 3
			}
		}
		// Exact phrase match bonus
		if strings.Contains(titleLower, queryLower) {
			score += 25
		}
		if strings.Contains(summaryLower, queryLower) {
			score += 10
		}

		if score > 0 {
			results = append(results, scored{article: a, score: score})
		}
	}

	if len(results) == 0 {
		return "No matching articles found in the CoreDial knowledge base for: " + query, nil
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Fetch top N article contents
	limit := maxSearchResults
	if len(results) < limit {
		limit = len(results)
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("Knowledge base results for \"%s\" (%d matches, showing top %d):\n\n", query, len(results), limit))

	for i := 0; i < limit; i++ {
		a := results[i].article
		content, err := fetchArticleContent(a.Slug)
		if err != nil {
			buf.WriteString(fmt.Sprintf("--- Article %d: %s ---\n", i+1, a.Title))
			buf.WriteString(fmt.Sprintf("(Could not fetch content: %v)\n", err))
			if a.Summary != "" {
				buf.WriteString(fmt.Sprintf("Summary: %s\n", a.Summary))
			}
			buf.WriteString("\n")
			continue
		}

		buf.WriteString(fmt.Sprintf("--- Article %d: %s ---\n", i+1, a.Title))
		// Trim to reasonable size for context
		if len(content) > 4000 {
			content = content[:4000] + "\n... (truncated)"
		}
		buf.WriteString(content)
		buf.WriteString("\n\n")
	}

	return buf.String(), nil
}

// fetchArticleContent retrieves the text content of a single CoreDial article.
func fetchArticleContent(slug string) (string, error) {
	url := fmt.Sprintf("%s/en/articles/%s", coreDialBaseURL, slug)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch article %s: %w", slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("article %s returned status %d", slug, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read article %s: %w", slug, err)
	}

	return extractArticleText(string(body)), nil
}

// Precompiled regexes for HTML processing.
var (
	bodyRe   = regexp.MustCompile(`"body"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	scriptRe = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRe  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	tagRe    = regexp.MustCompile(`<[^>]+>`)
	wsRe     = regexp.MustCompile(`\s+`)
)

// extractArticleText strips HTML tags and extracts readable text from an
// article page. We do a simple but effective tag-strip since the content
// is relatively clean documentation HTML.
func extractArticleText(html string) string {
	// Try to find the article body in the initialData first
	// Articles embed their body as HTML in the JSON
	if m := bodyRe.FindStringSubmatch(html); m != nil {
		decoded := unescapeJSON(m[1])
		return stripHTML(decoded)
	}

	// Fallback: strip the whole page
	return stripHTML(html)
}

// stripHTML removes HTML tags and normalises whitespace.
func stripHTML(s string) string {
	s = scriptRe.ReplaceAllString(s, "")
	s = styleRe.ReplaceAllString(s, "")
	s = tagRe.ReplaceAllString(s, " ")

	// Decode common HTML entities
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "\\u0026", "&")
	s = strings.ReplaceAll(s, "\\u003c", "<")
	s = strings.ReplaceAll(s, "\\u003e", ">")

	// Collapse whitespace
	s = wsRe.ReplaceAllString(s, " ")

	return strings.TrimSpace(s)
}

// coreDialToolDef is the Grok function/tool definition for the CoreDial search.
// Used in both text chat (REST API) and voice (Realtime API).
var coreDialToolDef = map[string]interface{}{
	"type": "function",
	"function": map[string]interface{}{
		"name":        "search_coredial_kb",
		"description": "Search the internal knowledge base for technical documentation about VoIP, phone provisioning, networking, SIP trunking, number porting, call routing, and other platform features. Use this when the user asks a technical question about phone configuration, VoIP setup, or troubleshooting. IMPORTANT: After receiving search results, you MUST summarize the answer directly in your own words. Never tell the user to visit a website, click a link, or look up the article themselves. Never share docs.coredial.com URLs. You are the expert — give them the complete answer based on what you found.",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The search query — use specific technical terms like 'voicemail setup', 'SIP trunking', 'Yealink provisioning', 'number porting', etc.",
				},
			},
			"required": []string{"query"},
		},
	},
}
