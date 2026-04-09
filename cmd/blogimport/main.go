// Command blogimport mirrors the WordPress blog at
// pcsblogpost.datalytique.com/pcsblogpost into static HTML pages on the PCS
// VoIP site, downloading every featured + inline image so the result is fully
// self-contained. It is intended to be re-runnable: every invocation
// regenerates the blog/ directory and the assets/img/blog/ tree, picking up
// any new posts on the source side.
//
// Output:
//
//	blog/index.html, blog/page-2.html, blog/page-3.html, ...   (paginated index)
//	blog/<slug>.html                                             (one per post)
//	assets/img/blog/<slug>/cover.<ext>                           (featured image)
//	assets/img/blog/<slug>/<basename>                            (inline images)
//
// Run from the project root:
//
//	go run ./cmd/blogimport -out .
package main

import (
	"crypto/sha1"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	apiBase           = "https://pcsblogpost.datalytique.com/pcsblogpost/wp-json/wp/v2"
	postsPerIndexPage = 9
	httpTimeout       = 30 * time.Second
)

//go:embed templates/*.html.tmpl
var templateFS embed.FS

// =====================================================================
// WordPress REST types
// =====================================================================

type WPPost struct {
	ID            int       `json:"id"`
	Date          string    `json:"date"` // 2025-02-03T11:44:16
	Slug          string    `json:"slug"`
	Link          string    `json:"link"`
	Title         WPRendered `json:"title"`
	Content       WPRendered `json:"content"`
	Excerpt       WPRendered `json:"excerpt"`
	FeaturedMedia int       `json:"featured_media"`
	Embedded      WPEmbed   `json:"_embedded"`
}

type WPRendered struct {
	Rendered string `json:"rendered"`
}

type WPEmbed struct {
	FeaturedMedia []WPMedia `json:"wp:featuredmedia"`
}

type WPMedia struct {
	SourceURL     string         `json:"source_url"`
	AltText       string         `json:"alt_text"`
	MediaDetails  WPMediaDetails `json:"media_details"`
}

type WPMediaDetails struct {
	Sizes map[string]WPMediaSize `json:"sizes"`
}

type WPMediaSize struct {
	SourceURL string `json:"source_url"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// pickCoverURL returns the smallest source_url that is at least 800 px wide.
// Falls back to "large", "medium_large", "medium", then the full source_url.
func (m WPMedia) pickCoverURL() string {
	preferences := []string{"large", "medium_large", "medium"}
	for _, name := range preferences {
		if s, ok := m.MediaDetails.Sizes[name]; ok && s.SourceURL != "" {
			return s.SourceURL
		}
	}
	return m.SourceURL
}

// =====================================================================
// Template view models
// =====================================================================

type PostView struct {
	Title       string
	Slug        string
	DateText    string
	DateISO     string
	Author      string
	Cover       string // /assets/img/blog/<slug>/cover.<ext>
	CoverAlt    string
	Body        template.HTML
	Excerpt     string
	OriginalURL string
	PrevURL     string
	NextURL     string
	PrevTitle   string
	NextTitle   string
}

type IndexView struct {
	Title       string
	PageHeading string
	Posts       []PostView
	Page        int
	LastPage    int
	Total       int
	HasPrev     bool
	HasNext     bool
	PrevURL     string
	NextURL     string
}

// =====================================================================
// Main
// =====================================================================

func main() {
	outDir := flag.String("out", ".", "project root (where blog/ and assets/ live)")
	flag.Parse()

	log.SetFlags(log.Ltime)
	log.Printf("blogimport: source = %s", apiBase)

	posts, err := fetchAllPosts()
	if err != nil {
		log.Fatalf("fetch posts: %v", err)
	}
	log.Printf("blogimport: fetched %d posts", len(posts))

	mediaIndex, err := fetchMediaIndex()
	if err != nil {
		log.Printf("blogimport: media index unavailable (%v) — inline images will use raw URLs", err)
		mediaIndex = map[string]WPMedia{}
	} else {
		log.Printf("blogimport: indexed %d media items for size resolution", len(mediaIndex))
	}

	// Newest first.
	sort.Slice(posts, func(i, j int) bool { return posts[i].Date > posts[j].Date })

	blogDir := filepath.Join(*outDir, "blog")
	mediaDir := filepath.Join(*outDir, "assets", "img", "blog", "_media")
	if err := os.MkdirAll(blogDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", blogDir, err)
	}
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", mediaDir, err)
	}

	tmpl, err := template.New("blog").Funcs(template.FuncMap{
		"safe": func(s string) template.HTML { return template.HTML(s) },
	}).ParseFS(templateFS, "templates/*.html.tmpl")
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}

	views := make([]PostView, 0, len(posts))
	for _, p := range posts {
		v, err := processPost(p, mediaDir, mediaIndex)
		if err != nil {
			log.Printf("blogimport: skip %q: %v", p.Slug, err)
			continue
		}
		views = append(views, v)
	}
	log.Printf("blogimport: downloaded %d unique images into %s", len(mediaByBase), filepath.ToSlash(mediaDir))

	// Wire prev/next within the chronological list.
	for i := range views {
		if i > 0 {
			views[i].PrevURL = "./" + views[i-1].Slug + ".html"
			views[i].PrevTitle = views[i-1].Title
		}
		if i < len(views)-1 {
			views[i].NextURL = "./" + views[i+1].Slug + ".html"
			views[i].NextTitle = views[i+1].Title
		}
	}

	for _, v := range views {
		if err := renderPost(blogDir, v, tmpl); err != nil {
			log.Printf("blogimport: render post %q: %v", v.Slug, err)
		}
	}
	if err := renderIndexes(blogDir, views, tmpl); err != nil {
		log.Fatalf("render indexes: %v", err)
	}

	pages := (len(views) + postsPerIndexPage - 1) / postsPerIndexPage
	if pages < 1 {
		pages = 1
	}
	log.Printf("blogimport: wrote %d posts and %d index page(s)", len(views), pages)
}

// =====================================================================
// Fetch
// =====================================================================

var httpc = &http.Client{Timeout: httpTimeout}

// Bluehost / mod_security blocks the Go default User-Agent. Use a real one.
const userAgent = "Mozilla/5.0 (PegasiBlogImporter/1.0; +https://pcsvoip.datalytique.com)"

func httpGet(u string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, image/*, */*")
	return httpc.Do(req)
}

func fetchAllPosts() ([]WPPost, error) {
	var all []WPPost
	for page := 1; ; page++ {
		batch, hasMore, err := fetchPage(page)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if !hasMore {
			break
		}
	}
	return all, nil
}

// fetchMediaIndex walks /wp/v2/media and returns a map keyed by the basename
// of each item's full source URL. The values carry every size variant WP
// generated, so we can swap "full" inline image URLs in body content for
// smaller "large" or "medium_large" renditions.
func fetchMediaIndex() (map[string]WPMedia, error) {
	out := make(map[string]WPMedia)
	for page := 1; page < 20; page++ { // safety cap
		u := fmt.Sprintf("%s/media?per_page=100&page=%d", apiBase, page)
		resp, err := httpGet(u)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == 400 || resp.StatusCode == 404 {
			resp.Body.Close()
			break
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			resp.Body.Close()
			return nil, fmt.Errorf("media HTTP %d: %s", resp.StatusCode, string(body))
		}
		var items []WPMedia
		if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
			resp.Body.Close()
			return nil, err
		}
		totalPages := 0
		if h := resp.Header.Get("X-WP-TotalPages"); h != "" {
			fmt.Sscanf(h, "%d", &totalPages)
		}
		resp.Body.Close()

		for _, m := range items {
			base := basenameFromURL(m.SourceURL)
			if base != "" && base != "image" {
				out[base] = m
			}
		}
		if totalPages > 0 && page >= totalPages {
			break
		}
		if len(items) < 100 {
			break
		}
	}
	return out, nil
}

// resolveSmallerVariant looks up an image URL in the media index and, if
// found, returns the URL of its "large" or "medium_large" variant. Returns
// the original URL when no smaller version exists or the lookup misses.
func resolveSmallerVariant(rawURL string, index map[string]WPMedia) string {
	if rawURL == "" || len(index) == 0 {
		return rawURL
	}
	base := basenameFromURL(rawURL)
	item, ok := index[base]
	if !ok {
		return rawURL
	}
	preferences := []string{"large", "medium_large"}
	for _, name := range preferences {
		if s, ok := item.MediaDetails.Sizes[name]; ok && s.SourceURL != "" {
			return s.SourceURL
		}
	}
	return rawURL
}

func fetchPage(page int) ([]WPPost, bool, error) {
	u := fmt.Sprintf("%s/posts?per_page=100&page=%d&_embed=wp:featuredmedia", apiBase, page)
	resp, err := httpGet(u)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 400 || resp.StatusCode == 404 {
		// Past the last page.
		return nil, false, nil
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, false, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var posts []WPPost
	if err := json.NewDecoder(resp.Body).Decode(&posts); err != nil {
		return nil, false, fmt.Errorf("decode: %w", err)
	}
	totalPages := 0
	if h := resp.Header.Get("X-WP-TotalPages"); h != "" {
		fmt.Sscanf(h, "%d", &totalPages)
	}
	hasMore := totalPages > 0 && page < totalPages
	return posts, hasMore, nil
}

// =====================================================================
// Per-post processing
// =====================================================================

var (
	leadingDateP = regexp.MustCompile(`(?is)^\s*<p\b[^>]*text-align\s*:\s*right[^>]*>.*?</p>\s*`)
	scriptTag    = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	onAttr       = regexp.MustCompile(`(?i)\s+on[a-z]+\s*=\s*("[^"]*"|'[^']*')`)
	imgTag       = regexp.MustCompile(`(?i)<img\b[^>]*>`)
	srcAttr      = regexp.MustCompile(`(?i)\bsrc\s*=\s*"([^"]+)"`)
	srcsetAttr   = regexp.MustCompile(`(?i)\s+srcset\s*=\s*"[^"]*"`)
	htmlTagStrip = regexp.MustCompile(`<[^>]+>`)
	whitespace   = regexp.MustCompile(`\s+`)
)

func processPost(p WPPost, mediaDir string, mediaIndex map[string]WPMedia) (PostView, error) {
	if strings.TrimSpace(p.Slug) == "" {
		return PostView{}, fmt.Errorf("missing slug")
	}
	t, err := time.Parse("2006-01-02T15:04:05", p.Date)
	if err != nil {
		t = time.Now()
	}

	// Featured image — prefer the WordPress "large" rendition (~1024 px) over
	// the raw "full" upload to keep weight reasonable. Deduped via mediaCache.
	cover := ""
	coverAlt := ""
	if len(p.Embedded.FeaturedMedia) > 0 {
		fm := p.Embedded.FeaturedMedia[0]
		coverURL := fm.pickCoverURL()
		if coverURL != "" {
			localPath, err := downloadImage(coverURL, mediaDir)
			if err != nil {
				log.Printf("blogimport: cover image %q (%s): %v", p.Slug, coverURL, err)
			} else {
				cover = "/" + filepath.ToSlash(localPath)
				coverAlt = fm.AltText
			}
		}
	}

	// Sanitize content.
	content := p.Content.Rendered
	content = leadingDateP.ReplaceAllString(content, "")
	content = scriptTag.ReplaceAllString(content, "")
	content = onAttr.ReplaceAllString(content, "")
	content = srcsetAttr.ReplaceAllString(content, "")

	// Mirror inline images, also deduped. Each src is first swapped to a
	// smaller WordPress-generated variant when one is available.
	content = imgTag.ReplaceAllStringFunc(content, func(tag string) string {
		m := srcAttr.FindStringSubmatch(tag)
		if m == nil || len(m) < 2 {
			return tag
		}
		src := m[1]
		if !strings.HasPrefix(src, "http") {
			return tag
		}
		smaller := resolveSmallerVariant(src, mediaIndex)
		localPath, err := downloadImage(smaller, mediaDir)
		if err != nil {
			log.Printf("blogimport: inline image %q (%s): %v", p.Slug, smaller, err)
			return tag
		}
		newSrc := "/" + filepath.ToSlash(localPath)
		return strings.Replace(tag, src, newSrc, 1)
	})

	view := PostView{
		Title:       html.UnescapeString(p.Title.Rendered),
		Slug:        p.Slug,
		DateText:    t.Format("January 2, 2006"),
		DateISO:     t.Format("2006-01-02"),
		Author:      "PCS VoIP Team",
		Cover:       cover,
		CoverAlt:    coverAlt,
		Body:        template.HTML(content),
		Excerpt:     makeExcerpt(content),
		OriginalURL: p.Link,
	}
	return view, nil
}

func makeExcerpt(htmlContent string) string {
	plain := htmlTagStrip.ReplaceAllString(htmlContent, " ")
	plain = html.UnescapeString(plain)
	plain = whitespace.ReplaceAllString(plain, " ")
	plain = strings.TrimSpace(plain)
	if len(plain) > 220 {
		plain = plain[:220] + "…"
	}
	return plain
}

// =====================================================================
// Image download
// =====================================================================

func basenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "image"
	}
	base := path.Base(u.Path)
	if base == "" || base == "/" || base == "." {
		return "image"
	}
	// Sanitize: lowercase, drop spaces, keep alphanumerics + . _ -
	clean := strings.Builder{}
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			clean.WriteRune(r)
		case r == ' ':
			clean.WriteByte('-')
		}
	}
	return clean.String()
}

// mediaCache dedupes downloads across the whole import. Two layers of
// deduplication:
//   - by source URL (exact match): a quick path
//   - by sanitized basename: collapses the same image referenced through
//     different host aliases (pcsblogpost.datalytique.com vs www.pcsvoip.com)
//
// All downloads land in a single assets/img/blog/_media directory.
var (
	mediaMu       sync.Mutex
	mediaByURL    = map[string]string{} // src URL -> project-relative path
	mediaByBase   = map[string]string{} // sanitized basename -> project-relative path
)

// downloadImage stores src into mediaDir, deduplicating first by URL then by
// basename. Returns the path relative to the project root.
func downloadImage(src, mediaDir string) (string, error) {
	mediaMu.Lock()
	if cached, ok := mediaByURL[src]; ok {
		mediaMu.Unlock()
		return cached, nil
	}

	base := basenameFromURL(src)
	if base == "image" || filepath.Ext(base) == "" {
		h := sha1.Sum([]byte(src))
		base = "img-" + hex.EncodeToString(h[:])[:10] + ".jpg"
	}
	if cached, ok := mediaByBase[base]; ok {
		// Same filename already downloaded (likely the same image via a
		// different URL). Reuse it and remember the URL → path mapping.
		mediaByURL[src] = cached
		mediaMu.Unlock()
		return cached, nil
	}
	mediaMu.Unlock()

	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(mediaDir, base)
	rel := relProject(dest)

	// Idempotent: existing non-empty file is fine.
	if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
		mediaMu.Lock()
		mediaByURL[src] = rel
		mediaByBase[base] = rel
		mediaMu.Unlock()
		return rel, nil
	}

	resp, err := httpGet(src)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > 1500*1024 {
		log.Printf("blogimport: large image %.1f MB: %s", float64(resp.ContentLength)/(1024*1024), src)
	}
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}

	mediaMu.Lock()
	mediaByURL[src] = rel
	mediaByBase[base] = rel
	mediaMu.Unlock()
	return rel, nil
}

// relProject converts an absolute filesystem path to a project-relative one
// (e.g. "assets/img/blog/foo/cover.png"). Used in URL construction.
func relProject(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return p
	}
	return rel
}

// =====================================================================
// Render
// =====================================================================

func renderPost(blogDir string, v PostView, tmpl *template.Template) error {
	dest := filepath.Join(blogDir, v.Slug+".html")
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.ExecuteTemplate(f, "post.html.tmpl", v)
}

func renderIndexes(blogDir string, posts []PostView, tmpl *template.Template) error {
	total := len(posts)
	pages := (total + postsPerIndexPage - 1) / postsPerIndexPage
	if pages < 1 {
		pages = 1
	}
	for p := 1; p <= pages; p++ {
		start := (p - 1) * postsPerIndexPage
		end := start + postsPerIndexPage
		if end > total {
			end = total
		}
		view := IndexView{
			Title:       fmt.Sprintf("Blog · Page %d of %d", p, pages),
			PageHeading: "PCS VoIP Blog",
			Posts:       posts[start:end],
			Page:        p,
			LastPage:    pages,
			Total:       total,
			HasPrev:     p > 1,
			HasNext:     p < pages,
		}
		if p > 1 {
			if p == 2 {
				view.PrevURL = "./index.html"
			} else {
				view.PrevURL = fmt.Sprintf("./page-%d.html", p-1)
			}
		}
		if p < pages {
			view.NextURL = fmt.Sprintf("./page-%d.html", p+1)
		}
		var dest string
		if p == 1 {
			dest = filepath.Join(blogDir, "index.html")
		} else {
			dest = filepath.Join(blogDir, fmt.Sprintf("page-%d.html", p))
		}
		f, err := os.Create(dest)
		if err != nil {
			return err
		}
		if err := tmpl.ExecuteTemplate(f, "index.html.tmpl", view); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	return nil
}
