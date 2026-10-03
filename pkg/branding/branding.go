// Package branding resolves the organisation branding Gorilla UI shows: policy
// registry values first, then the config.yaml `branding:` block, field by
// field. Every field is validated here, in the SYSTEM service, so the UI only
// ever receives plain text, an http(s) URL, a #rrggbb colour and a sniffed
// image. A field that fails validation is dropped; it never fails the request.
package branding

import (
	"bytes"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/1dustindavis/gorilla/pkg/config"
)

// Caps on the admin-supplied values. Text is counted in runes.
const (
	maxTitle     = 120
	maxTagline   = 240
	maxHelpLabel = 60
	maxHelpURL   = 2048
	MaxLogoBytes = 512 << 10
)

var accentPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Branding is the GetBranding payload. Unset or rejected fields are empty.
type Branding struct {
	Title      string `json:"title"`
	Tagline    string `json:"tagline"`
	HelpURL    string `json:"helpUrl"`
	HelpLabel  string `json:"helpLabel"`
	Accent     string `json:"accent"`
	LogoMime   string `json:"logoMime"`
	LogoBase64 string `json:"logoBase64"`
}

// Resolve reads the policy registry values, merges them over cfg and returns
// the validated branding. The logo file is read on every call, so a replaced
// logo shows up without a service restart.
func Resolve(cfg config.Branding) Branding {
	return resolve(merge(readPolicy(), cfg))
}

// merge returns policy's value for every field it sets, else cfg's. A policy
// value that later fails validation is dropped, not replaced by cfg's.
func merge(policy, cfg config.Branding) config.Branding {
	pick := func(p, c string) string {
		if strings.TrimSpace(p) != "" {
			return p
		}
		return c
	}
	return config.Branding{
		Title:     pick(policy.Title, cfg.Title),
		Tagline:   pick(policy.Tagline, cfg.Tagline),
		Logo:      pick(policy.Logo, cfg.Logo),
		HelpURL:   pick(policy.HelpURL, cfg.HelpURL),
		HelpLabel: pick(policy.HelpLabel, cfg.HelpLabel),
		Accent:    pick(policy.Accent, cfg.Accent),
	}
}

func resolve(in config.Branding) Branding {
	out := Branding{
		Title:     plainText(in.Title, maxTitle),
		Tagline:   plainText(in.Tagline, maxTagline),
		HelpURL:   helpURL(in.HelpURL),
		HelpLabel: plainText(in.HelpLabel, maxHelpLabel),
	}
	if accent := strings.TrimSpace(in.Accent); accentPattern.MatchString(accent) {
		out.Accent = strings.ToLower(accent)
	} else if accent != "" {
		slog.Debug("branding accent dropped", "accent", accent)
	}
	if path := strings.TrimSpace(in.Logo); path != "" {
		out.LogoMime, out.LogoBase64 = logo(path)
	}
	return out
}

// plainText trims, drops control characters (including newlines) and caps
// the value at limit runes.
func plainText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if runes := []rune(value); len(runes) > limit {
		value = strings.TrimSpace(string(runes[:limit]))
	}
	return value
}

func helpURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || len(value) > maxHelpURL || parsed.Host == "" ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
		slog.Debug("branding help_url dropped", "helpUrl", value, "err", err)
		return ""
	}
	return parsed.String()
}

// logo reads the local file at path and returns its MIME type and base64
// bytes, or empty strings when it is missing, too large or not a PNG, JPEG or
// SVG. The UI only ever renders it through <img>, where an SVG cannot run
// script.
func logo(path string) (string, string) {
	file, err := os.Open(path)
	if err != nil {
		slog.Debug("branding logo dropped", "path", path, "err", err)
		return "", ""
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, MaxLogoBytes+1))
	if err != nil || len(data) == 0 || len(data) > MaxLogoBytes {
		slog.Debug("branding logo dropped", "path", path, "bytes", len(data), "err", err)
		return "", ""
	}
	mime := sniffImage(data)
	if mime == "" {
		slog.Debug("branding logo dropped", "path", path, "reason", "not a PNG, JPEG or SVG")
		return "", ""
	}
	return mime, base64.StdEncoding.EncodeToString(data)
}

func sniffImage(data []byte) string {
	switch detected := http.DetectContentType(data); detected {
	case "image/png", "image/jpeg":
		return detected
	}
	if isSVG(data) {
		return "image/svg+xml"
	}
	return ""
}

// isSVG accepts markup whose first element is <svg>, after an optional BOM,
// XML declaration, doctype and comments. http.DetectContentType reports SVG
// as text/xml or text/plain, so it cannot tell on its own.
func isSVG(data []byte) bool {
	rest := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	for {
		rest = bytes.TrimLeft(rest, " \t\r\n")
		switch {
		case hasPrefixFold(rest, "<svg"):
			return len(rest) > 4 && (rest[4] == ' ' || rest[4] == '>' || rest[4] == '\t' || rest[4] == '\r' || rest[4] == '\n')
		case bytes.HasPrefix(rest, []byte("<?")):
			rest = skipPast(rest, "?>")
		case bytes.HasPrefix(rest, []byte("<!--")):
			rest = skipPast(rest, "-->")
		case hasPrefixFold(rest, "<!doctype"):
			rest = skipPast(rest, ">")
		default:
			return false
		}
		if rest == nil {
			return false
		}
	}
}

func hasPrefixFold(data []byte, prefix string) bool {
	return len(data) >= len(prefix) && strings.EqualFold(string(data[:len(prefix)]), prefix)
}

func skipPast(data []byte, marker string) []byte {
	i := bytes.Index(data, []byte(marker))
	if i < 0 {
		return nil
	}
	return data[i+len(marker):]
}
