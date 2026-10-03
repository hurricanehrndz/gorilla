//go:build windows

package branding

import (
	"errors"
	"log/slog"

	"golang.org/x/sys/windows/registry"

	"github.com/1dustindavis/gorilla/pkg/config"
)

// The policy key MDM or GPO writes. Variables only so the Windows test can
// point the reader at a scratch key under HKCU.
var (
	policyRoot = registry.LOCAL_MACHINE
	policyPath = `SOFTWARE\Policies\Gorilla\Branding`
)

// readPolicy returns the REG_SZ values under the policy key. A missing key or
// value reads as unset.
func readPolicy() config.Branding {
	key, err := registry.OpenKey(policyRoot, policyPath, registry.QUERY_VALUE)
	if err != nil {
		if !errors.Is(err, registry.ErrNotExist) {
			slog.Debug("branding policy key unreadable", "path", policyPath, "err", err)
		}
		return config.Branding{}
	}
	defer func() { _ = key.Close() }()
	value := func(name string) string {
		s, _, err := key.GetStringValue(name)
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			slog.Debug("branding policy value dropped", "name", name, "err", err)
		}
		return s
	}
	return config.Branding{
		Title:     value("Title"),
		Tagline:   value("Tagline"),
		Logo:      value("LogoPath"),
		HelpURL:   value("HelpUrl"),
		HelpLabel: value("HelpLabel"),
		Accent:    value("Accent"),
	}
}
