package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestGet tests that the configuration is retrieved and parsed properly
func TestGet(t *testing.T) {
	// Define what we expect in a successful test
	expected := Configuration{
		URL:             "https://example.com/gorilla/",
		URLPackages:     "https://example.com/gorilla/",
		Manifest:        "example_manifest",
		LocalManifests:  []string{"example_local_manifest"},
		Catalogs:        []string{"example_catalog"},
		AppDataPath:     filepath.Clean("c:/cpe/gorilla/"),
		Verbose:         true,
		Debug:           true,
		CheckOnly:       true,
		AuthUser:        "johnny",
		AuthPass:        "pizza",
		CachePath:       filepath.Clean("c:/cpe/gorilla/cache"),
		ServiceMode:     false,
		ServiceCommand:  "",
		ServiceInstall:  false,
		ServiceRemove:   false,
		ServiceStart:    false,
		ServiceStop:     false,
		ServiceStatus:   false,
		ServiceName:     "gorilla",
		ServiceInterval: "1h",
		ServicePipeName: "gorilla-service",
		Branding: Branding{
			Title:     "Acme Software Center",
			Tagline:   "Need help? Call the service desk at ext. 1234.",
			Logo:      `C:\ProgramData\gorilla\branding\logo.png`,
			HelpURL:   "https://example.com/help",
			HelpLabel: "Get help",
			Accent:    "#0b6e4f",
		},
		ConfigPath: "testdata/test_config.yaml",
	}

	// Save the original arguments
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// Override with our input
	os.Args = []string{"gorilla.exe", "-config", "testdata/test_config.yaml"}

	// Run the actual code
	cfg := Get()

	// Compare the result with our expectations
	structsMatch := reflect.DeepEqual(expected, cfg)

	if !structsMatch {
		t.Errorf("\n\nExpected:\n\n%#v\n\nReceived:\n\n%#v", expected, cfg)
	}
}

// TestGetIgnoresLegacyRepoPath proves configs written for the removed -build
// mode, which still carry repo_path, keep loading for the agent.
func TestGetIgnoresLegacyRepoPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "legacy_config.yaml")
	configYAML := []byte(`
url: https://example.com/gorilla/
manifest: example_manifest
app_data_path: c:/cpe/gorilla/
repo_path: c:/repo/gorilla
`)
	if err := os.WriteFile(configPath, configYAML, 0o644); err != nil {
		t.Fatal(err)
	}

	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	origExit := osExit
	defer func() { osExit = origExit }()
	osExit = func(code int) { t.Fatalf("unexpected exit %d", code) }

	os.Args = []string{"gorilla.exe", "--config", configPath}
	cfg := Get()

	if cfg.Manifest != "example_manifest" || cfg.URL != "https://example.com/gorilla/" {
		t.Fatalf("legacy config not parsed: %#v", cfg)
	}
}

// TestGetRequiresManifestAndURL covers the normal-run validation. It used to
// sit behind a build/import exemption; now every non-service run needs both.
func TestGetRequiresManifestAndURL(t *testing.T) {
	tests := map[string]string{
		"no manifest": "url: https://example.com/gorilla/\n",
		"no url":      "manifest: example_manifest\n",
	}
	for name, configYAML := range tests {
		t.Run(name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(configPath, []byte(configYAML), 0o644); err != nil {
				t.Fatal(err)
			}

			origArgs := os.Args
			defer func() { os.Args = origArgs }()
			origExit := osExit
			defer func() { osExit = origExit }()

			// osExit must stop Get, as os.Exit would, so unwind with a panic.
			type exitCode int
			osExit = func(code int) { panic(exitCode(code)) }

			os.Args = []string{"gorilla.exe", "--config", configPath}
			code := func() (code exitCode) {
				defer func() {
					if r := recover(); r != nil {
						code = r.(exitCode)
					}
				}()
				Get()
				return 0
			}()
			if code != 1 {
				t.Fatalf("expected exit 1, got %d", code)
			}
		})
	}
}

// TestParseArguments tests if flag is parsed correctly
func TestParseArguments(t *testing.T) {
	// Set our expectations
	expectedConfig := `.\fake.yaml`
	expectedVerbose := true
	expectedDebug := true
	expectedCheckOnly := true

	// Save the original arguments
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// Override with our input
	os.Args = []string{"gorilla.exe", "--verbose", "--debug", "--checkonly", "--config", `.\fake.yaml`}

	// Run code
	configArg, verboseArg, debugArg, checkonlyArg := parseArguments()

	// Compare config
	if have, want := configArg, expectedConfig; have != want {
		t.Errorf("have %s, want %s", have, want)
	}

	// Compare checkonly
	if have, want := checkonlyArg, expectedCheckOnly; have != want {
		t.Errorf("have %v, want %v", have, want)
	}

	// Compare verbose
	if have, want := verboseArg, expectedVerbose; have != want {
		t.Errorf("have %v, want %v", have, want)
	}

	// Compare debug
	if have, want := debugArg, expectedDebug; have != want {
		t.Errorf("have %v, want %v", have, want)
	}
}

// Example tests if help is is parsed properly
func Example() {
	// Save the original osExit
	origExit := osExit
	defer func() { osExit = origExit }()

	// Override with a fake exit
	// var exitCode int
	osExit = func(code int) {
		_ = code
	}

	// Save the original arguments
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// Override with our input
	os.Args = []string{"gorilla.exe", "--help"}

	// Run code, ignoring the return values
	_, _, _, _ = parseArguments()

	// Output:
	// unknown unknown
	//
	// Gorilla - Munki-like Application Management for Windows
	// https://github.com/1dustindavis/gorilla
	//
	// Usage: gorilla.exe [options]
	//
	// Options:
	// -c, -config         path to configuration file in yaml format
	// -C, -checkonly	    enable check only mode
	// -v, -verbose        enable verbose output
	// -d, -debug          enable debug output
	// -a, -about          displays the version number and other build info
	// -V, -version        display the version number
	// -s, -service        run Gorilla as a Windows service
	// -S, -servicecmd     send a command to a running Gorilla service (ListOptionalInstalls|GetBranding|InstallItem:itemName|RemoveItem:itemName|StreamOperationStatus:operationId|CancelOperation:operationId)
	// -serviceinstall     install Gorilla as a Windows service
	// -serviceremove      remove Gorilla Windows service
	// -servicestart       start Gorilla Windows service
	// -servicestop        stop Gorilla Windows service
	// -servicestatus      show Gorilla Windows service status
	// -h, -help           display this help message
}
