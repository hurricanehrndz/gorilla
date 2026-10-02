package manifest

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/admin"
	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"go.yaml.in/yaml/v4"
)

var expectedCatalog = make(map[string]catalog.Item)

func fakeCatalogDownload(string string) ([]byte, error) {
	fmt.Println(string)

	// Generate yaml from the expectedCatalog map
	yamlBytes, err := yaml.Marshal(expectedCatalog)
	if err != nil {
		return nil, err
	}

	return yamlBytes, nil
}

func fakeDownloadByURL(payloads map[string][]byte, failures map[string]error) func(string) ([]byte, error) {
	return func(url string) ([]byte, error) {
		if err, ok := failures[url]; ok {
			return nil, err
		}
		if body, ok := payloads[url]; ok {
			return body, nil
		}
		return nil, fmt.Errorf("unexpected URL in test: %s", url)
	}
}

// TestGetCatalogsParsesCatalog verifies that a valid catlog is parsed correctly and returns the expected map
func TestGetCatalogsParsesCatalog(t *testing.T) {
	expectedCatalog = make(map[string]catalog.Item)
	// Set what we expect GetCatalogs() to return
	expectedCatalog[`ChefClient`] = catalog.Item{
		Name:          "ChefClient",
		Dependencies:  []string{`ruby`},
		DisplayName:   "Chef Client",
		Description:   "Chef configuration management client",
		Category:      "Utilities",
		Developer:     "Chef Software",
		IconName:      "chef.png",
		RestartAction: "RequireRestart",
		UpdateFor:     []string{"ruby"},
		Check: catalog.InstallCheck{
			File: []catalog.FileCheck{{Path: `C:\opscode\chef\bin\chef-client.bat`}, {Path: `C:\test\path\check\file.exe`, Hash: `abc1234567890def`, Version: `1.2.3.0`}},
			Script: `$latest = "14.3.37"
$current = C:\opscode\chef\bin\chef-client.bat --version
$current = $current.Split(" ")[1]
$upToDate = [System.Version]$current -ge [System.Version]$latest
If ($upToDate) {
  exit 1
} Else {
  exit 0
}
`,
		},
		Installer: catalog.InstallerItem{
			Arguments: []string{`/L=1033`, `/S`},
			Hash:      `f5ef8c31898592824751ec2252fe317c0f667db25ac40452710c8ccf35a1b28d`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64.msi`,
		},
		Uninstaller:         catalog.InstallerItem{Type: `msi`, Arguments: []string{`/S`}},
		Version:             `68.0.3440.106`,
		BlockingApps:        []string{"test"},
		PreUninstallScript:  "echo pre-uninstall",
		PostUninstallScript: "echo post-uninstall",
	}

	// Define a Configuration struct to pass to `GetCatalogs`
	cfg := config.Configuration{
		URL:       "https://example.com/",
		Manifest:  "example_manifest",
		CachePath: "testdata/",
		Catalogs:  []string{"test_catalog"},
	}

	// Override the downloadFile function with our fake function
	origDownload := downloadGet
	defer func() { downloadGet = origDownload }()
	downloadGet = fakeCatalogDownload

	// Run `GetCatalogs`
	testCatalog, err := GetCatalogs(cfg)
	if err != nil {
		t.Fatalf("GetCatalogs() failed: %v", err)
	}

	mapsMatch := reflect.DeepEqual(expectedCatalog, testCatalog[1])

	if !mapsMatch {
		t.Errorf("\n\nExpected:\n\n%#v\n\nReceived:\n\n %#v", expectedCatalog, testCatalog[1])
	}
}

func TestGetCatalogsReturnsErrorForMissingCatalog(t *testing.T) {
	baseCatalog := map[string]catalog.Item{
		"Chrome": {
			DisplayName: "Chrome",
			Installer: catalog.InstallerItem{
				Type:     "nupkg",
				Location: "packages/chrome/chrome.nupkg",
				Hash:     "abc",
			},
		},
	}
	baseYAML, err := yaml.Marshal(baseCatalog)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Configuration{
		URL:      "https://example.com/",
		Catalogs: []string{"base", "missing"},
	}

	origDownload := downloadGet
	defer func() { downloadGet = origDownload }()
	downloadGet = fakeDownloadByURL(
		map[string][]byte{
			"https://example.com/catalogs/base.yaml": baseYAML,
		},
		map[string]error{
			"https://example.com/catalogs/missing.yaml": errors.New("404"),
		},
	)

	_, err = GetCatalogs(cfg)
	if err == nil {
		t.Fatalf("expected GetCatalogs() to fail for missing catalog")
	}
}

func TestGetCatalogsReturnsErrorForInvalidYAML(t *testing.T) {
	baseCatalog := map[string]catalog.Item{
		"ChefClient": {
			DisplayName: "Chef Client",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "packages/chef/chef.msi",
				Hash:     "abc",
			},
		},
	}
	baseYAML, err := yaml.Marshal(baseCatalog)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Configuration{
		URL:      "https://example.com/",
		Catalogs: []string{"valid", "broken"},
	}

	origDownload := downloadGet
	defer func() { downloadGet = origDownload }()
	downloadGet = fakeDownloadByURL(
		map[string][]byte{
			"https://example.com/catalogs/valid.yaml":  baseYAML,
			"https://example.com/catalogs/broken.yaml": []byte(":\n- not valid yaml"),
		},
		nil,
	)

	_, err = GetCatalogs(cfg)
	if err == nil {
		t.Fatalf("expected GetCatalogs() to fail for invalid catalog YAML")
	}
}

func TestGetCatalogsNoCatalogsReturnsError(t *testing.T) {
	cfg := config.Configuration{
		URL:      "https://example.com/",
		Catalogs: []string{},
	}

	_, err := GetCatalogs(cfg)
	if err == nil {
		t.Fatalf("expected error when no catalogs are configured")
	}
}

// TestGetCatalogsReadsBuildCatalogsOutput proves the agent loader reads what the
// repository tooling writes.
func TestGetCatalogsReadsBuildCatalogsOutput(t *testing.T) {
	repoPath := t.TempDir()
	packagesInfoPath := filepath.Join(repoPath, "packages-info")
	if err := os.MkdirAll(packagesInfoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	item := `
item_name: Chrome
display_name: Google Chrome
catalog: base
check:
  registry:
    name: Google Chrome
    version: 1.2.3.4
installer:
  type: nupkg
  location: packages/chrome/chrome.nupkg
  hash: abc
version: 1.2.3.4
`
	if err := os.WriteFile(filepath.Join(packagesInfoPath, "chrome.yaml"), []byte(item), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := admin.BuildCatalogs(repoPath); err != nil {
		t.Fatalf("BuildCatalogs failed: %v", err)
	}

	handler := http.NewServeMux()
	handler.Handle("/catalogs/", http.StripPrefix("/catalogs/", http.FileServer(http.Dir(filepath.Join(repoPath, "catalogs")))))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	cfg := config.Configuration{
		URL:       ts.URL + "/",
		Catalogs:  []string{"base"},
		CachePath: t.TempDir(),
	}
	got, err := GetCatalogs(cfg)
	if err != nil {
		t.Fatalf("GetCatalogs failed: %v", err)
	}
	baseCatalog, ok := got[1]
	if !ok {
		t.Fatalf("expected catalog map at index 1")
	}
	chrome, ok := baseCatalog["Chrome"]
	if !ok {
		t.Fatalf("expected Chrome item in catalog")
	}
	if chrome.DisplayName != "Google Chrome" {
		t.Fatalf("unexpected display_name: %s", chrome.DisplayName)
	}
	if chrome.Installer.Type != "nupkg" {
		t.Fatalf("unexpected installer type: %s", chrome.Installer.Type)
	}
	if chrome.Version != "1.2.3.4" {
		t.Fatalf("unexpected version: %s", chrome.Version)
	}
}
