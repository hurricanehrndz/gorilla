package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/branding"
	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/report"
)

// TestGetOptionalItemsHonestStatus exercises the enriched ListOptionalInstalls
// payload (R9): every status branch plus the script-only and no-catalog fallbacks
// to Unknown, driven by real file checks through one shared status.Checker.
func TestGetOptionalItemsHonestStatus(t *testing.T) {
	appData := filepath.Clean(t.TempDir())
	cfg := config.Configuration{AppDataPath: appData, CachePath: t.TempDir(), Catalogs: []string{"selfserve"}}

	// Two markers on disk make "installed" real; the missing ones are not.
	present := filepath.Join(t.TempDir(), "present.txt")
	if err := os.WriteFile(present, []byte("x"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	presentRemove := filepath.Join(t.TempDir(), "present-remove.txt")
	if err := os.WriteFile(presentRemove, []byte("x"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.txt")

	fileCheck := func(path string) catalog.InstallCheck {
		return catalog.InstallCheck{File: []catalog.FileCheck{{Path: path}}}
	}

	origManifest, origCatalog := manifestGet, catalogGet
	t.Cleanup(func() { manifestGet, catalogGet = origManifest, origCatalog })
	manifestGet = func(_ config.Configuration) ([]manifest.Item, []string, error) {
		return []manifest.Item{{Name: "base", OptionalInstalls: []string{
			"Installed", "PendingRemove", "WillInstall", "NotInstalled", "ScriptOnly", "NoCatalog",
		}}}, nil, nil
	}
	catalogGet = func(_ config.Configuration) (map[int]map[string]catalog.Item, error) {
		return map[int]map[string]catalog.Item{1: {
			"Installed":     {Name: "Installed", DisplayName: "Installed App", Version: "1.0", Description: "d", Category: "c", Developer: "dev", Check: fileCheck(present)},
			"PendingRemove": {Name: "PendingRemove", Check: fileCheck(presentRemove)},
			"WillInstall":   {Name: "WillInstall", Check: fileCheck(missing)},
			"NotInstalled":  {Name: "NotInstalled", Check: fileCheck(missing)},
			"ScriptOnly":    {Name: "ScriptOnly", Check: catalog.InstallCheck{Script: "exit 0"}},
		}}, nil
	}

	// Selected + pending-removal self-serve state.
	ssPath := manifest.SelfServePath(appData)
	if err := manifest.SaveSelfServe(ssPath, manifest.Item{Installs: []string{"WillInstall"}, Uninstalls: []string{"PendingRemove"}}); err != nil {
		t.Fatalf("save self-serve: %v", err)
	}

	items, err := getOptionalItems(cfg)
	if err != nil {
		t.Fatalf("getOptionalItems failed: %v", err)
	}

	byName := make(map[string]OptionalInstallItem, len(items))
	for _, it := range items {
		byName[it.ItemName] = it
	}
	wantStatus := map[string]string{
		"Installed":     "Installed",
		"PendingRemove": "WillBeRemoved",
		"WillInstall":   "WillBeInstalled",
		"NotInstalled":  "NotInstalled",
		"ScriptOnly":    "Unknown",
		"NoCatalog":     "Unknown",
	}
	for name, want := range wantStatus {
		got, ok := byName[name]
		if !ok {
			t.Errorf("missing item %q in payload", name)
			continue
		}
		if got.Status != want {
			t.Errorf("item %q status = %q, want %q", name, got.Status, want)
		}
	}
	if byName["Installed"].Catalog != "selfserve" || byName["Installed"].Description != "d" {
		t.Errorf("Installed item missing catalog/metadata: %#v", byName["Installed"])
	}
	if byName["WillInstall"].IsManaged != true {
		t.Errorf("WillInstall should be managed (selected)")
	}
	if byName["ScriptOnly"].IsInstalled {
		t.Errorf("script-only item must report isInstalled=false")
	}
}

func TestParseCommandSpecInstallItem(t *testing.T) {
	cmd, err := parseCommandSpec("InstallItem:GoogleChrome")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cmd.Action != actionInstallItem {
		t.Fatalf("expected action %s, got %s", actionInstallItem, cmd.Action)
	}
	if len(cmd.Items) != 1 || cmd.Items[0] != "GoogleChrome" {
		t.Fatalf("unexpected items: %#v", cmd.Items)
	}
}

func TestParseCommandSpecRemoveItem(t *testing.T) {
	cmd, err := parseCommandSpec("RemoveItem:GoogleChrome")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cmd.Action != actionRemoveItem {
		t.Fatalf("expected action %s, got %s", actionRemoveItem, cmd.Action)
	}
	if len(cmd.Items) != 1 || cmd.Items[0] != "GoogleChrome" {
		t.Fatalf("unexpected items: %#v", cmd.Items)
	}
}

func TestParseCommandSpecListOptionalInstalls(t *testing.T) {
	cmd, err := parseCommandSpec("ListOptionalInstalls")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cmd.Action != actionListOptionalInstalls {
		t.Fatalf("expected action %s, got %s", actionListOptionalInstalls, cmd.Action)
	}
	if len(cmd.Items) != 0 {
		t.Fatalf("expected no items, got %#v", cmd.Items)
	}
}

func TestParseCommandSpecStreamOperationStatus(t *testing.T) {
	cmd, err := parseCommandSpec("StreamOperationStatus:op-123")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cmd.Action != actionStreamOperationStatus {
		t.Fatalf("expected action %s, got %s", actionStreamOperationStatus, cmd.Action)
	}
	if len(cmd.Items) != 1 || cmd.Items[0] != "op-123" {
		t.Fatalf("unexpected items: %#v", cmd.Items)
	}
}

func TestParseCommandSpecInvalid(t *testing.T) {
	_, err := parseCommandSpec("InstallItem")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestParseCommandSpecLegacyActionInvalid(t *testing.T) {
	_, err := parseCommandSpec("install:foo")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateCommandRunWithItems(t *testing.T) {
	err := validateCommand(Command{
		Action: actionRun,
		Items:  []string{"foo"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateCommandInstallItemRequiresOneArgument(t *testing.T) {
	err := validateCommand(Command{
		Action: actionInstallItem,
		Items:  []string{"foo", "bar"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestExecuteCommandRunPassesProgressCallback(t *testing.T) {
	called := false
	progress := func(catalog.Item, string, int, string) { called = true }
	_, err := executeCommand(config.Configuration{}, Command{Action: actionRun, progress: progress}, func(_ config.Configuration, got installer.ProgressFn, _ *installer.Cancels) (*report.Report, error) {
		got(catalog.Item{}, "installing", 50, "")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if !called {
		t.Fatal("managed run did not receive command progress callback")
	}
}

func TestServiceInstallArgs(t *testing.T) {
	configPath := `C:\ProgramData\gorilla\config.yaml`
	got := serviceInstallArgs(configPath)
	if len(got) != 3 {
		t.Fatalf("expected 3 args, got %d: %#v", len(got), got)
	}
	if got[0] != "-c" {
		t.Fatalf("expected first arg -c, got %q", got[0])
	}
	if got[1] != configPath {
		t.Fatalf("expected config path %q, got %q", configPath, got[1])
	}
	if got[2] != "-service" {
		t.Fatalf("expected final arg -service, got %q", got[2])
	}
}

func TestParseCommandSpecGetBranding(t *testing.T) {
	cmd, err := parseCommandSpec("getbranding")
	if err != nil || cmd.Action != actionGetBranding || len(cmd.Items) != 0 {
		t.Fatalf("parse GetBranding = %#v, %v", cmd, err)
	}
	if _, err := parseCommandSpec("GetBranding:extra"); err == nil {
		t.Fatal("GetBranding accepted an argument")
	}
}

// GetBranding resolves the config block in the service (off Windows there is
// no policy store), and validation drops a bad field without failing the call.
func TestExecuteCommandGetBranding(t *testing.T) {
	cfg := config.Configuration{Branding: config.Branding{Title: " Acme ", Accent: "green", HelpURL: "javascript:alert(1)"}}
	resp, err := executeCommand(cfg, Command{Action: actionGetBranding}, nil)
	if err != nil || resp.Branding == nil {
		t.Fatalf("GetBranding = %#v, %v", resp, err)
	}
	if want := (branding.Branding{Title: "Acme"}); *resp.Branding != want {
		t.Fatalf("branding = %#v, want %#v", *resp.Branding, want)
	}
}

func TestBrandingSummaryReplacesLogoWithSize(t *testing.T) {
	line, err := brandingSummary(branding.Branding{Title: "Acme", LogoMime: "image/png", LogoBase64: "AAECAw=="})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(line, "logoBase64") || !strings.Contains(line, `"logoBytes":4`) || !strings.Contains(line, `"title":"Acme"`) {
		t.Fatalf("summary %s", line)
	}
}

func TestParseCommandSpecCancelOperation(t *testing.T) {
	cmd, err := parseCommandSpec("canceloperation:12345")
	if err != nil || cmd.Action != actionCancelOperation || len(cmd.Items) != 1 || cmd.Items[0] != "12345" {
		t.Fatalf("parseCommandSpec = %#v, %v", cmd, err)
	}
	if _, err := parseCommandSpec("CancelOperation"); err == nil {
		t.Fatal("CancelOperation without an operationId must be rejected")
	}
}
