package branding

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/config"
)

// A policy value must win field by field, so an MDM can override one field
// (say the title) while the config block still supplies the rest.
func TestMergePolicyWinsPerField(t *testing.T) {
	cfg := config.Branding{Title: "Config", Tagline: "Config tagline", Accent: "#111111", HelpURL: "https://config.example/"}
	policy := config.Branding{Title: "Policy", Accent: "  ", HelpURL: "https://policy.example/"}
	got := merge(policy, cfg)
	want := config.Branding{Title: "Policy", Tagline: "Config tagline", Accent: "#111111", HelpURL: "https://policy.example/"}
	if got != want {
		t.Fatalf("merge = %#v, want %#v", got, want)
	}
}

func TestResolveNothingConfiguredIsEmpty(t *testing.T) {
	if got := resolve(config.Branding{}); got != (Branding{}) {
		t.Fatalf("empty branding resolved to %#v", got)
	}
}

func TestResolveValidatesText(t *testing.T) {
	got := resolve(config.Branding{
		Title:     "  Acme\r\nSoftware\tCenter  ",
		Tagline:   strings.Repeat("é", maxTagline+5),
		HelpLabel: " Get help ",
	})
	if got.Title != "AcmeSoftwareCenter" {
		t.Errorf("title %q kept control characters", got.Title)
	}
	if n := len([]rune(got.Tagline)); n != maxTagline {
		t.Errorf("tagline has %d runes, want the %d cap", n, maxTagline)
	}
	if got.HelpLabel != "Get help" {
		t.Errorf("help label %q not trimmed", got.HelpLabel)
	}
}

// The help link is opened in the system browser, so anything but an absolute
// http(s) URL (javascript:, file:, a bare path) must never reach the UI.
func TestResolveHelpURL(t *testing.T) {
	for value, want := range map[string]string{
		"https://example.invalid/help": "https://example.invalid/help",
		" HTTP://intranet/help ":       "http://intranet/help",
		"javascript:alert(1)":          "",
		"file:///C:/Windows/notepad":   "",
		"/help":                        "",
		"https://":                     "",
		"https://x/" + strings.Repeat("a", maxHelpURL): "",
	} {
		if got := resolve(config.Branding{HelpURL: value}).HelpURL; got != want {
			t.Errorf("help url %q resolved to %q, want %q", value, got, want)
		}
	}
}

func TestResolveAccent(t *testing.T) {
	for value, want := range map[string]string{
		"#0B6E4F":       "#0b6e4f",
		" #0b6e4f ":     "#0b6e4f",
		"0b6e4f":        "",
		"#0b6e4":        "",
		"red":           "",
		"#0b6e4f; x:y":  "",
		"var(--accent)": "",
	} {
		if got := resolve(config.Branding{Accent: value}).Accent; got != want {
			t.Errorf("accent %q resolved to %q, want %q", value, got, want)
		}
	}
}

// pngHeader is enough for http.DetectContentType to call it a PNG.
var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveLogo(t *testing.T) {
	svg := []byte("\xef\xbb\xbf<?xml version=\"1.0\"?>\n<!-- logo -->\n<svg xmlns=\"http://www.w3.org/2000/svg\"/>")
	tests := []struct {
		name     string
		path     string
		wantMime string
	}{
		{"png", writeFile(t, "logo.png", pngHeader), "image/png"},
		{"jpeg", writeFile(t, "logo.jpg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF")), "image/jpeg"},
		{"svg", writeFile(t, "logo.svg", svg), "image/svg+xml"},
		{"html named png", writeFile(t, "evil.png", []byte("<html><script>alert(1)</script>")), ""},
		{"svg-like element", writeFile(t, "x.svg", []byte("<svgfoo/>")), ""},
		{"empty", writeFile(t, "empty.png", nil), ""},
		{"too large", writeFile(t, "big.png", append(pngHeader, make([]byte, MaxLogoBytes)...)), ""},
		{"missing", filepath.Join(t.TempDir(), "missing.png"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolve(config.Branding{Title: "kept", Logo: tt.path})
			if got.LogoMime != tt.wantMime {
				t.Fatalf("mime %q, want %q", got.LogoMime, tt.wantMime)
			}
			if got.Title != "kept" {
				t.Fatal("a bad logo must not drop the other fields")
			}
			if tt.wantMime == "" {
				if got.LogoBase64 != "" {
					t.Fatal("rejected logo still carried bytes")
				}
				return
			}
			want, _ := os.ReadFile(tt.path)
			if decoded, _ := base64.StdEncoding.DecodeString(got.LogoBase64); string(decoded) != string(want) {
				t.Fatal("logo bytes did not round-trip")
			}
		})
	}
}
