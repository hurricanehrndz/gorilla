//go:build windows

package branding

import (
	"testing"

	"golang.org/x/sys/windows/registry"

	"github.com/1dustindavis/gorilla/pkg/config"
)

// The reader runs against a scratch HKCU key so the test needs no admin token
// and never touches a real policy.
func TestReadPolicyFromRegistry(t *testing.T) {
	path := `Software\GorillaTest\` + t.Name()
	policyRoot, policyPath = registry.CURRENT_USER, path
	t.Cleanup(func() {
		policyRoot, policyPath = registry.LOCAL_MACHINE, `SOFTWARE\Policies\Gorilla\Branding`
		_ = registry.DeleteKey(registry.CURRENT_USER, path)
	})

	if got := readPolicy(); got != (config.Branding{}) {
		t.Fatalf("missing key read as %#v", got)
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"Title": "Policy Title", "Tagline": "T", "LogoPath": `C:\logo.png`,
		"HelpUrl": "https://example.invalid/help", "HelpLabel": "Help", "Accent": "#0b6e4f",
	} {
		if err := key.SetStringValue(name, value); err != nil {
			t.Fatal(err)
		}
	}
	_ = key.Close()

	want := config.Branding{
		Title: "Policy Title", Tagline: "T", Logo: `C:\logo.png`,
		HelpURL: "https://example.invalid/help", HelpLabel: "Help", Accent: "#0b6e4f",
	}
	if got := readPolicy(); got != want {
		t.Fatalf("readPolicy = %#v, want %#v", got, want)
	}
}
