package process

import (
	"errors"
	"reflect"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/status"
)

func TestReconcileSelfServe(t *testing.T) {
	tests := []struct {
		name           string
		selfServe      manifest.Item
		manifests      []manifest.Item
		wantInstalls   []string
		wantUninstalls []string
		wantSelfInst   []string // expected selfServe.Installs after reconcile
		wantDefaults   []string // expected selfServe.DefaultInstalls after reconcile
		wantChanged    bool
	}{
		{
			name:         "authorization filters unavailable and warns-and-keeps",
			selfServe:    manifest.Item{Installs: []string{"Allowed", "Rogue"}},
			manifests:    []manifest.Item{{OptionalInstalls: []string{"Allowed"}}},
			wantInstalls: []string{"Allowed"},
			// Rogue stays in the file even though it is not authorized.
			wantSelfInst: []string{"Allowed", "Rogue"},
			wantChanged:  false,
		},
		{
			// A default that is also offered as optional installs (Munki
			// convention: defaults are defaults among optionals).
			name:         "default asserted once, added to installs and record",
			selfServe:    manifest.Item{},
			manifests:    []manifest.Item{{OptionalInstalls: []string{"Deemo"}, DefaultInstalls: []string{"Deemo"}}},
			wantInstalls: []string{"Deemo"},
			wantSelfInst: []string{"Deemo"},
			wantDefaults: []string{"Deemo"},
			wantChanged:  true,
		},
		{
			// Default-only name (never offered as optional): asserted into the
			// self-serve file but filtered out of the returned installs, as in
			// Munki (analyze.py:747-748, core.py:264-278).
			name:         "default-only asserted but filtered from installs",
			selfServe:    manifest.Item{},
			manifests:    []manifest.Item{{DefaultInstalls: []string{"Deemo"}}},
			wantInstalls: nil,
			wantSelfInst: []string{"Deemo"},
			wantDefaults: []string{"Deemo"},
			wantChanged:  true,
		},
		{
			name: "user-removed default never re-asserts",
			// Deemo already recorded but user removed it from Installs.
			selfServe:    manifest.Item{DefaultInstalls: []string{"Deemo"}},
			manifests:    []manifest.Item{{DefaultInstalls: []string{"Deemo"}}},
			wantInstalls: nil,
			wantSelfInst: nil,
			wantDefaults: []string{"Deemo"},
			wantChanged:  false,
		},
		{
			name:           "uninstalls pass through verbatim",
			selfServe:      manifest.Item{Uninstalls: []string{"GoneItem"}},
			manifests:      []manifest.Item{{OptionalInstalls: []string{"Allowed"}}},
			wantUninstalls: []string{"GoneItem"},
			wantChanged:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ss := tc.selfServe
			installs, uninstalls, changed := ReconcileSelfServe(&ss, tc.manifests)
			if !reflect.DeepEqual(installs, tc.wantInstalls) {
				t.Errorf("installs: want %#v got %#v", tc.wantInstalls, installs)
			}
			if !reflect.DeepEqual(uninstalls, tc.wantUninstalls) {
				t.Errorf("uninstalls: want %#v got %#v", tc.wantUninstalls, uninstalls)
			}
			if changed != tc.wantChanged {
				t.Errorf("changed: want %v got %v", tc.wantChanged, changed)
			}
			if tc.wantSelfInst != nil || ss.Installs != nil {
				if !reflect.DeepEqual(ss.Installs, tc.wantSelfInst) {
					t.Errorf("selfServe.Installs: want %#v got %#v", tc.wantSelfInst, ss.Installs)
				}
			}
			if tc.wantDefaults != nil && !reflect.DeepEqual(ss.DefaultInstalls, tc.wantDefaults) {
				t.Errorf("selfServe.DefaultInstalls: want %#v got %#v", tc.wantDefaults, ss.DefaultInstalls)
			}
		})
	}
}

func TestPruneSelfServeUninstalls(t *testing.T) {
	catalogs := map[int]map[string]catalog.Item{1: {
		"Gone": catalog.Item{
			DisplayName: "Gone",
			Uninstaller: catalog.InstallerItem{Type: "msi", Location: "gone.msi"},
		},
		"StillHere": catalog.Item{
			DisplayName: "StillHere",
			Uninstaller: catalog.InstallerItem{Type: "msi", Location: "still.msi"},
		},
		"Errored": catalog.Item{
			DisplayName: "Errored",
			Uninstaller: catalog.InstallerItem{Type: "msi", Location: "err.msi"},
		},
	}}

	// Stub the status seam: Gone is not installed (prunes); StillHere is still
	// installed (kept); Errored returns an error (kept). Unresolvable stays too.
	orig := statusCheck
	defer func() { statusCheck = orig }()
	statusCheck = func(_ *status.Checker, item catalog.Item, installType, _ string) (bool, error) {
		if installType != "uninstall" {
			t.Fatalf("expected uninstall check, got %q", installType)
		}
		switch item.DisplayName {
		case "Gone":
			return false, nil
		case "StillHere":
			return true, nil
		case "Errored":
			return false, errors.New("boom")
		}
		return false, nil
	}

	ss := manifest.Item{Uninstalls: []string{"Gone", "StillHere", "Errored", "Unresolvable"}}
	changed := PruneSelfServeUninstalls(&ss, catalogs, &status.Checker{}, "cache")
	if !changed {
		t.Fatalf("expected changed=true")
	}
	want := []string{"StillHere", "Errored", "Unresolvable"}
	if !reflect.DeepEqual(ss.Uninstalls, want) {
		t.Fatalf("unexpected kept uninstalls: want %#v got %#v", want, ss.Uninstalls)
	}
}
