package admin

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
)

// MaxExtractedTextLen guards against runaway uploads.
const MaxExtractedTextLen = 200_000

// AllowedDocExtensions lists the file types we accept on the KB upload form.
var AllowedDocExtensions = map[string]bool{
	".txt":  true,
	".md":   true,
	".csv":  true,
	".pdf":  true,
	".docx": true,
}

// IsAllowedDoc reports whether the filename has a supported extension.
func IsAllowedDoc(name string) bool {
	return AllowedDocExtensions[strings.ToLower(filepath.Ext(name))]
}

// ExtractText reads filename and returns the cleaned plain-text contents.
// Returned text is truncated to MaxExtractedTextLen.
func ExtractText(filename string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	var (
		text string
		err  error
	)
	switch ext {
	case ".txt", ".md", ".csv":
		text, err = extractPlain(filename)
	case ".pdf":
		text, err = extractPDF(filename)
	case ".docx":
		text, err = extractDOCX(filename)
	default:
		return "", fmt.Errorf("unsupported file type: %s", ext)
	}
	if err != nil {
		return "", err
	}
	text = normalizeWhitespace(text)
	if len(text) > MaxExtractedTextLen {
		text = text[:MaxExtractedTextLen] + "\n\n[truncated]"
	}
	return text, nil
}

func extractPlain(name string) (string, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func extractPDF(name string) (string, error) {
	f, r, err := pdf.Open(name)
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		t, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		sb.WriteString(t)
		sb.WriteString("\n")
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "", fmt.Errorf("no extractable text in PDF (may be image-based)")
	}
	return out, nil
}

func extractDOCX(name string) (string, error) {
	rc, err := zip.OpenReader(name)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer rc.Close()

	for _, f := range rc.File {
		if f.Name != "word/document.xml" {
			continue
		}
		fd, err := f.Open()
		if err != nil {
			return "", err
		}
		defer fd.Close()
		return parseDocxXML(fd)
	}
	return "", fmt.Errorf("word/document.xml not found in DOCX")
}

func parseDocxXML(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	var sb strings.Builder
	inText := false
	paraCount := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText = true
			} else if t.Name.Local == "p" {
				if paraCount > 0 {
					sb.WriteString("\n\n")
				}
				paraCount++
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		}
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "", fmt.Errorf("no text content in DOCX")
	}
	return out, nil
}

var multiNewline = regexp.MustCompile(`\n{3,}`)

func normalizeWhitespace(s string) string {
	return multiNewline.ReplaceAllString(s, "\n\n")
}
