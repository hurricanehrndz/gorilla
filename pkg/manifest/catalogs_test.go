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

// TestGetCatalogsRoundTrip proves the agent loader reads back exactly what
// makecatalogs writes, now that catalog output omits empty fields.
func TestGetCatalogsRoundTrip(t *testing.T) {
	full := catalog.Item{
		Dependencies: []string{"ruby"},
		DisplayName:  "Chef Client",
		Check: catalog.InstallCheck{
			File:     []catalog.FileCheck{{Path: `C:\chef\chef.bat`, Version: "1.2.3", ProductName: "Chef", Hash: "abc"}},
			Script:   "exit 0",
			Registry: catalog.RegCheck{Name: "Chef Client", Version: "1.2.3"},
			Appx:     catalog.AppxCheck{Name: "Chef.Appx", Version: "1.2.3.0"},
		},
		Installer:           catalog.InstallerItem{Type: "msi", Location: "packages/chef.msi", Hash: "def", PackageID: "chef", Arguments: []string{"/S"}},
		Uninstaller:         catalog.InstallerItem{Type: "msi", Location: "packages/chef.msi", Hash: "def", PackageID: "chef", Arguments: []string{"/x"}},
		Version:             "1.2.3",
		BlockingApps:        []string{"chef"},
		UpdateFor:           []string{"ruby"},
		Description:         "Configuration management",
		Category:            "Utilities",
		Developer:           "Chef Software",
		IconName:            "chef.png",
		RestartAction:       "RequireRestart",
		PreScript:           "echo pre",
		PostScript:          "echo post",
		PreUninstallScript:  "echo preun",
		PostUninstallScript: "echo postun",
	}
	minimal := catalog.Item{DisplayName: "Minimal", Version: "1.0"}

	// Write package-info files the way an admin would, one per item.
	repoPath := t.TempDir()
	packagesInfoPath := filepath.Join(repoPath, "packages-info")
	if err := os.MkdirAll(packagesInfoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	type packageInfo struct {
		ItemName string       `yaml:"item_name"`
		Catalog  string       `yaml:"catalog"`
		Item     catalog.Item `yaml:",inline"`
	}
	for _, info := range []packageInfo{{"ChefClient", "full", full}, {"Minimal", "minimal", minimal}} {
		body, err := yaml.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packagesInfoPath, info.ItemName+".yaml"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	set, err := admin.CollectCatalogs(repoPath)
	if err != nil {
		t.Fatalf("CollectCatalogs failed: %v", err)
	}
	if err = admin.WriteCatalogs(repoPath, set); err != nil {
		t.Fatalf("WriteCatalogs failed: %v", err)
	}

	// An empty field written out would read back as a non-nil empty value; for
	// Check.File that silently switches status checks to the file method.
	minimalYAML, err := os.ReadFile(filepath.Join(repoPath, "catalogs", "minimal.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]map[string]any
	if err = yaml.Unmarshal(minimalYAML, &keys); err != nil {
		t.Fatal(err)
	}
	if want := (map[string]map[string]any{"Minimal": {"display_name": "Minimal", "version": "1.0"}}); !reflect.DeepEqual(keys, want) {
		t.Fatalf("minimal catalog has extra keys:\n%s", minimalYAML)
	}

	handler := http.NewServeMux()
	handler.Handle("/catalogs/", http.StripPrefix("/catalogs/", http.FileServer(http.Dir(filepath.Join(repoPath, "catalogs")))))
	ts := httptest.NewServer(handler)
	defer ts.Close()

	got, err := GetCatalogs(config.Configuration{URL: ts.URL + "/", Catalogs: []string{"full", "minimal"}})
	if err != nil {
		t.Fatalf("GetCatalogs failed: %v", err)
	}

	// GetCatalogs stamps each item with its catalog key.
	full.Name, minimal.Name = "ChefClient", "Minimal"
	want := map[int]map[string]catalog.Item{1: {"ChefClient": full}, 2: {"Minimal": minimal}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch\nwant %#v\ngot  %#v", want, got)
	}
}
