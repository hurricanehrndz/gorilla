package status

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/gorillalog"
)

var (
	// store original data to restore after each test
	origExec = execCommand

	// Temp directory for logging
	logTmp, _ = os.MkdirTemp("", "gorilla-status_test")

	// Setup a testing Configuration struct
	cfgVerbose = config.Configuration{
		Debug:       false,
		Verbose:     true,
		AppDataPath: logTmp,
	}

	// fakeRegistryItems provides fake items for testing checkRegistry
	fakeRegistryItems = map[string]RegistryApplication{
		`registryCheckItem`: {
			Name:    `Registry Check Item`,
			Version: `1.2.0.3`,
		},
		`registryCheckItemOutdated`: {
			Name:    `Outdated`,
			Version: `33.6.3`,
		},
	}

	// These catalog items provide test data
	pathInstalled = catalog.Item{
		Check: catalog.InstallCheck{
			File: []catalog.FileCheck{{
				Path: `testdata/test_checkPath.msi`,
				Hash: `cc8f5a895f1c500aa3b4ae35f3878595f4587054a32fa6d7e9f46363525c59f9`,
			}},
		},
	}
	pathNotInstalled = catalog.Item{
		Check: catalog.InstallCheck{
			File: []catalog.FileCheck{{
				Path: `testdata/test_checkPath.msi`,
				Hash: `ba7d5a895f1c500aa3b4ae35f3878595f4587054a32fa6d7e9f46363525c59e8`,
			}},
		},
	}
	pathMissing = catalog.Item{
		Check: catalog.InstallCheck{
			File: []catalog.FileCheck{{
				Path: `testdata/bogus.msi`,
				Hash: `ba7d5a895f1c500aa3b4ae35f3878595f4587054a32fa6d7e9f46363525c59e8`,
			}},
		},
	}
	pathMetadataInstalled = catalog.Item{
		Check: catalog.InstallCheck{
			File: []catalog.FileCheck{{
				Path:        `testdata/test.exe`,
				Version:     `3.2.0.1`,
				ProductName: `Gorilla Test`,
			}},
		},
	}
	pathMetadataOutdated = catalog.Item{
		Check: catalog.InstallCheck{
			File: []catalog.FileCheck{{
				Path:        `testdata/test.exe`,
				Version:     `3.12.0.1`,
				ProductName: `Gorilla Test`,
			}},
		},
	}
	scriptActionNoError = catalog.Item{
		Installer: catalog.InstallerItem{Type: `ps1`},
	}
	scriptNoActionNoError = catalog.Item{
		Installer:   catalog.InstallerItem{Type: `ps1`},
		DisplayName: `scriptNoActionNoError`,
	}
	scriptCheckItem = catalog.Item{
		Check: catalog.InstallCheck{
			Script: `echo "pizza"`,
		},
		DisplayName: `scriptCheckItem`,
	}
	fileCheckItem = catalog.Item{
		Check: catalog.InstallCheck{
			File: []catalog.FileCheck{{
				Path: `testdata/test_checkPath.msi`,
			}},
		},
		DisplayName: `fileCheckItem`,
	}
	registryCheckItem = catalog.Item{
		Check: catalog.InstallCheck{
			Registry: catalog.RegCheck{
				Version: `1.2.0.3`,
				Name:    `Registry Check Item`,
			},
		},
		DisplayName: `registryCheckItem`,
	}
	registryCheckItemNotInstalled = catalog.Item{
		Check: catalog.InstallCheck{
			Registry: catalog.RegCheck{
				Version: `33.8.3`,
				Name:    `Not Installed`,
			},
		},
		DisplayName: `registryCheckItem`,
	}
	registryCheckItemOutdated = catalog.Item{
		Check: catalog.InstallCheck{
			Registry: catalog.RegCheck{
				Version: `33.12.0`,
				Name:    `Outdated`,
			},
		},
		DisplayName: `registryCheckItem`,
	}
	// appxCheckInstalled simulates a package that is current (version matches)
	appxCheckInstalled = catalog.Item{
		Check: catalog.InstallCheck{
			Appx: catalog.AppxCheck{
				Name:    statusActionNoError,
				Version: `1.2.0`,
			},
		},
		DisplayName: `appxCheckInstalled`,
	}
	// appxCheckOutdated simulates a package that is installed but below the required version
	appxCheckOutdated = catalog.Item{
		Check: catalog.InstallCheck{
			Appx: catalog.AppxCheck{
				Name:    statusActionNoError,
				Version: `9.9.9`,
			},
		},
		DisplayName: `appxCheckOutdated`,
	}
	// appxCheckNotInstalled simulates a package that is not registered
	appxCheckNotInstalled = catalog.Item{
		Check: catalog.InstallCheck{
			Appx: catalog.AppxCheck{
				Name:    statusNoActionNoError,
				Version: `1.0.0`,
			},
		},
		DisplayName: `appxCheckNotInstalled`,
	}
	appxCheckItem = catalog.Item{
		Check: catalog.InstallCheck{
			Appx: catalog.AppxCheck{
				Name:    `appxCheckItem`,
				Version: `1.0.0`,
			},
		},
		DisplayName: `appxCheckItem`,
	}
	noCheckItem = catalog.Item{
		DisplayName: `noCheckItem`,
	}

	// Define different options to bypass status checks during tests
	statusActionNoError   = `_gorilla_dev_action_noerror_`
	statusNoActionNoError = `_gorilla_dev_noaction_noerror_`
)

// check if a slice contains a string
func sliceContains(s []string, e string) bool {
	for _, a := range s {
		if strings.Contains(a, e) {
			return true
		}
	}
	return false
}

// fakeExecCommand provides a method for validating what is passed to exec.Command
// this function was copied verbatim from https://npf.io/2015/06/testing-exec-command/
func fakeExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	return cmd
}

// fakeExecCommandAppx returns a command that prints a version string when the
// package name matches statusActionNoError (simulating an installed package),
// or prints nothing when the name matches statusNoActionNoError (not installed).
func fakeExecCommandAppx(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcessAppx", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS_APPX=1"}
	return cmd
}

// TestHelperProcess processes the commands passed to fakeExecCommand
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if sliceContains(os.Args[3:], statusActionNoError) {
		os.Exit(0)
	}
	if sliceContains(os.Args[3:], statusNoActionNoError) {
		os.Exit(1)
	}
	os.Exit(0)
}

// TestHelperProcessAppx handles fake AppX version output for checkAppx tests.
// Prints "1.2.0" to stdout when the package name is statusActionNoError,
// prints nothing when the name is statusNoActionNoError (package not registered).
func TestHelperProcessAppx(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS_APPX") != "1" {
		return
	}
	if sliceContains(os.Args[3:], statusActionNoError) {
		fmt.Print("1.2.0")
	}
	// statusNoActionNoError: print nothing — package not installed
	os.Exit(0)
}

// TestCheckRegistry validates that the registry entries are checked properly
func TestCheckRegistry(t *testing.T) {
	// Seed a run-scoped checker with our fake registry items
	c := &Checker{registryItems: fakeRegistryItems}

	// install

	// Run checkRegistry with `registryCheckItem` as an `install`
	// We expect no action needed; Only error if action needed is true
	actionNeeded, _ := c.checkRegistry(registryCheckItem, "install")
	if actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkRegistry to return false", actionNeeded)
	}

	// Run checkRegistry with `registryCheckItemNotInstalled` as an `install`
	// We expect action is needed; Only error if action needed is false
	actionNeeded, _ = c.checkRegistry(registryCheckItemNotInstalled, "install")
	if !actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkRegistry to return true", actionNeeded)
	}

	// Run checkRegistry with `registryCheckItemOutdated` as an `install`
	// We expect action is needed; Only error if action needed is false
	actionNeeded, _ = c.checkRegistry(registryCheckItemOutdated, "install")
	if !actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkRegistry to return true", actionNeeded)
	}

	// uninstall

	// Run checkRegistry with `registryCheckItem` as an `uninstall`
	// We expect action is needed; Only error if action needed is false
	actionNeeded, _ = c.checkRegistry(registryCheckItem, "uninstall")
	if !actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkRegistry to return true", actionNeeded)
	}

	// Run checkRegistry with `registryCheckItemNotInstalled` as an `uninstall`
	// We expect no action needed; Only error if action needed is true
	actionNeeded, _ = c.checkRegistry(registryCheckItemNotInstalled, "uninstall")
	if actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkRegistry to return false", actionNeeded)
	}

	// update

	// Run checkRegistry with `registryCheckItem` as an `update`
	// We expect no action needed; Only error if action needed is true
	actionNeeded, _ = c.checkRegistry(registryCheckItem, "update")
	if actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkRegistry to return false", actionNeeded)
	}

	// Run checkRegistry with `registryCheckItemNotInstalled` as an `update`
	// We expect no action needed; Only error if action needed is true
	actionNeeded, _ = c.checkRegistry(registryCheckItemNotInstalled, "update")
	if actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkRegistry to return false", actionNeeded)
	}

	// Run checkRegistry with `registryCheckItemOutdated` as an `update`
	// We expect action is needed; Only error if action needed is false
	actionNeeded, _ = c.checkRegistry(registryCheckItemOutdated, "update")
	if !actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkRegistry to return true", actionNeeded)
	}
}

// TestCheckerCacheIsRunScoped validates that the registry cache lives on the
// Checker, so a fresh Checker for the next run re-populates instead of
// reusing the previous run's registry snapshot (K7)
func TestCheckerCacheIsRunScoped(t *testing.T) {
	// Run 1: checker seeded with an installed item
	run1 := &Checker{registryItems: fakeRegistryItems}
	actionNeeded, _ := run1.checkRegistry(registryCheckItem, "uninstall")
	if !actionNeeded {
		t.Fatalf("expected run 1 to see the item as installed")
	}

	// Run 2: a fresh checker must not see run 1's cache; it re-populates from
	// getUninstallKeys (empty on this platform), so the item is not installed
	run2 := &Checker{}
	actionNeeded, _ = run2.checkRegistry(registryCheckItem, "uninstall")
	if actionNeeded {
		t.Errorf("fresh Checker reused a previous run's registry cache (K7)")
	}
}

// TestCheckRegistryMatching validates the registry name/version matching rules
// against defects K2 (substring name matches) and K3 (first-hit break and
// nil-version panic), plus the install/update/uninstall decision table.
func TestCheckRegistryMatching(t *testing.T) {
	regCheckItem := func(name, version string) catalog.Item {
		return catalog.Item{
			Check: catalog.InstallCheck{
				Registry: catalog.RegCheck{
					Name:    name,
					Version: version,
				},
			},
			DisplayName: name,
		}
	}

	tests := []struct {
		desc        string
		registry    map[string]RegistryApplication
		item        catalog.Item
		installType string
		want        bool
	}{
		// K2: a registry name containing the catalog name as a substring is not a match
		{
			desc: `substring name is not installed (K2)`,
			registry: map[string]RegistryApplication{
				`javaUpdater`: {Name: `Java Auto Updater`, Version: `1.0.0`},
			},
			item:        regCheckItem(`Java`, `1.0.0`),
			installType: `install`,
			want:        true,
		},
		// K2: matching is case-insensitive and ignores surrounding whitespace
		{
			desc: `normalized name matches (K2)`,
			registry: map[string]RegistryApplication{
				`java`: {Name: ` java `, Version: `1.0.0`},
			},
			item:        regCheckItem(`Java`, `1.0.0`),
			installType: `install`,
			want:        false,
		},
		// K3: with duplicate names, any entry at or above the catalog version
		// satisfies the check regardless of map iteration order
		{
			desc: `duplicate names, one current (K3)`,
			registry: map[string]RegistryApplication{
				`dupOld`: {Name: `Dup`, Version: `1.0.0`},
				`dupNew`: {Name: `Dup`, Version: `2.0.0`},
			},
			item:        regCheckItem(`Dup`, `2.0.0`),
			installType: `install`,
			want:        false,
		},
		// K3: an unparseable installed version must not panic and is not a version match
		{
			desc: `garbage installed version (K3)`,
			registry: map[string]RegistryApplication{
				`garbage`: {Name: `Garbage`, Version: `latest`},
			},
			item:        regCheckItem(`Garbage`, `1.0.0`),
			installType: `install`,
			want:        true,
		},
		// K3: an unparseable entry is skipped, not fatal, when a parseable duplicate satisfies
		{
			desc: `garbage duplicate plus current entry (K3)`,
			registry: map[string]RegistryApplication{
				`garbage`: {Name: `Dup`, Version: `latest`},
				`current`: {Name: `Dup`, Version: `1.0.0`},
			},
			item:        regCheckItem(`Dup`, `1.0.0`),
			installType: `install`,
			want:        false,
		},
		// An unparseable catalog version falls back to exact string equality
		{
			desc: `garbage catalog version, exact string match`,
			registry: map[string]RegistryApplication{
				`garbage`: {Name: `Garbage`, Version: `latest`},
			},
			item:        regCheckItem(`Garbage`, `latest`),
			installType: `install`,
			want:        false,
		},
		{
			desc: `garbage catalog version, string mismatch`,
			registry: map[string]RegistryApplication{
				`garbage`: {Name: `Garbage`, Version: `1.0.0`},
			},
			item:        regCheckItem(`Garbage`, `latest`),
			installType: `install`,
			want:        true,
		},
		// Decision table: install/update/uninstall semantics are preserved
		{
			desc: `decision table: install outdated`,
			registry: map[string]RegistryApplication{
				`app`: {Name: `App`, Version: `1.0.0`},
			},
			item:        regCheckItem(`App`, `2.0.0`),
			installType: `install`,
			want:        true,
		},
		{
			desc:        `decision table: update not installed`,
			registry:    fakeRegistryItems,
			item:        regCheckItem(`Not Installed`, `1.0.0`),
			installType: `update`,
			want:        false,
		},
		{
			desc: `decision table: update outdated`,
			registry: map[string]RegistryApplication{
				`app`: {Name: `App`, Version: `1.0.0`},
			},
			item:        regCheckItem(`App`, `2.0.0`),
			installType: `update`,
			want:        true,
		},
		{
			desc: `decision table: uninstall installed`,
			registry: map[string]RegistryApplication{
				`app`: {Name: `App`, Version: `1.0.0`},
			},
			item:        regCheckItem(`App`, `1.0.0`),
			installType: `uninstall`,
			want:        true,
		},
		{
			desc:        `decision table: uninstall not installed`,
			registry:    fakeRegistryItems,
			item:        regCheckItem(`Not Installed`, `1.0.0`),
			installType: `uninstall`,
			want:        false,
		},
	}

	// Repeat the table to shake out any dependence on map iteration order (K3)
	for i := 0; i < 20; i++ {
		for _, tt := range tests {
			c := &Checker{registryItems: tt.registry}
			actionNeeded, _ := c.checkRegistry(tt.item, tt.installType)
			if actionNeeded != tt.want {
				t.Errorf("%s: actionNeeded: %v; Expected checkRegistry to return %v", tt.desc, actionNeeded, tt.want)
			}
		}
	}
}

// TestCheckAppx validates AppX/MSIX package status checks across install/update/uninstall types
func TestCheckAppx(t *testing.T) {
	execCommand = fakeExecCommandAppx
	defer func() { execCommand = origExec }()

	// install — package installed at version 1.2.0, catalog wants 1.2.0 → no action
	actionNeeded, _ := checkAppx(appxCheckInstalled, "install")
	if actionNeeded {
		t.Errorf("actionNeeded: %v; expected false (installed, version current)", actionNeeded)
	}

	// install — package installed at version 1.2.0, catalog wants 9.9.9 → action needed
	actionNeeded, _ = checkAppx(appxCheckOutdated, "install")
	if !actionNeeded {
		t.Errorf("actionNeeded: %v; expected true (installed, version outdated)", actionNeeded)
	}

	// install — package not installed, catalog wants 1.0.0 → action needed
	actionNeeded, _ = checkAppx(appxCheckNotInstalled, "install")
	if !actionNeeded {
		t.Errorf("actionNeeded: %v; expected true (not installed)", actionNeeded)
	}

	// uninstall — package installed → action needed
	actionNeeded, _ = checkAppx(appxCheckInstalled, "uninstall")
	if !actionNeeded {
		t.Errorf("actionNeeded: %v; expected true (installed, uninstall needed)", actionNeeded)
	}

	// uninstall — package not installed → no action
	actionNeeded, _ = checkAppx(appxCheckNotInstalled, "uninstall")
	if actionNeeded {
		t.Errorf("actionNeeded: %v; expected false (not installed, nothing to uninstall)", actionNeeded)
	}

	// update — package installed at version 1.2.0, catalog wants 1.2.0 → no action
	actionNeeded, _ = checkAppx(appxCheckInstalled, "update")
	if actionNeeded {
		t.Errorf("actionNeeded: %v; expected false (installed, version current)", actionNeeded)
	}

	// update — package installed, catalog wants 9.9.9 → action needed
	actionNeeded, _ = checkAppx(appxCheckOutdated, "update")
	if !actionNeeded {
		t.Errorf("actionNeeded: %v; expected true (installed, version outdated)", actionNeeded)
	}

	// update — package not installed → no action (don't update something not present)
	actionNeeded, _ = checkAppx(appxCheckNotInstalled, "update")
	if actionNeeded {
		t.Errorf("actionNeeded: %v; expected false (not installed, skip update)", actionNeeded)
	}
}

// TestCheckScript validates that a script is properly written disk, ran, and then deleted
// and the status is retrieved properly.
func TestCheckScript(t *testing.T) {
	// Override execCommand with our fake version
	execCommand = fakeExecCommand
	defer func() {
		execCommand = origExec
	}()

	// Set cachepath and run checkScript for scriptActionNoError
	cachepath := fmt.Sprintf("testdata/%s/", statusActionNoError)
	actionNeeded, err := checkScript(scriptActionNoError, cachepath, "install")
	if !actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript to action and no error")
	}

	// Set cachepath and run checkScript for scriptNoActionNoError
	cachepath = fmt.Sprintf("testdata/%s/", statusActionNoError)
	actionNeeded, err = checkScript(scriptActionNoError, cachepath, "uninstall")
	if actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript to no action and no error")
	}

	// Set cachepath and run checkScript for scriptNoActionNoError
	cachepath = fmt.Sprintf("testdata/%s/", statusNoActionNoError)
	actionNeeded, err = checkScript(scriptNoActionNoError, cachepath, "install")
	if actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript to return no action and no error")
	}

	// Set cachepath and run checkScript for scriptActionNoError
	cachepath = fmt.Sprintf("testdata/%s/", statusNoActionNoError)
	actionNeeded, err = checkScript(scriptNoActionNoError, cachepath, "uninstall")
	if !actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript to action and no error")
	}

	// Set cachepath and run checkScript for scriptActionNoError as update
	cachepath = fmt.Sprintf("testdata/%s/", statusActionNoError)
	actionNeeded, err = checkScript(scriptActionNoError, cachepath, "update")
	if !actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript update to action and no error")
	}

	// Set cachepath and run checkScript for scriptNoActionNoError as update
	cachepath = fmt.Sprintf("testdata/%s/", statusNoActionNoError)
	actionNeeded, err = checkScript(scriptNoActionNoError, cachepath, "update")
	if actionNeeded || err != nil {
		fmt.Printf("action: %v; error: %v\n", actionNeeded, err)
		t.Errorf("Expected checkScript update to no action and no error")
	}
}

// TestCheckPath validates that the status of a path is checked correctly
func TestCheckPath(t *testing.T) {
	// Run checkPath for pathInstalled
	// We expect action is not needed; Only error if action needed is true
	actionNeeded, err := checkPath(pathInstalled, "install")
	if err != nil {
		t.Errorf("checkPath failed: %v", err)
	}
	if actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkPath to return false", actionNeeded)
	}

	// Run checkPath for file that doesn't exist
	// We expect action is not needed; Only error if action needed is true
	actionNeeded, err = checkPath(pathMissing, "update")
	if err != nil {
		t.Errorf("checkPath failed: %v", err)
	}
	if actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkPath to return false", actionNeeded)
	}

	// Run checkPath for pathNotInstalled
	// We expect action is needed; Only error if actionNeeded is false
	actionNeeded, err = checkPath(pathNotInstalled, "install")
	if err != nil {
		t.Error(err)
	}
	if !actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkPath to return true", actionNeeded)
	}

	// Run checkPath for pathMetadataInstalled
	// We expect action is not needed; Only error if actionNeeded is true
	actionNeeded, err = checkPath(pathMetadataInstalled, "install")
	if err != nil {
		t.Error(err)
	}
	if actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkPath to return false", actionNeeded)
	}

	// Run checkPath for pathMetadataOutdated
	// We expect action is needed; Only error if actionNeeded is false
	actionNeeded, err = checkPath(pathMetadataOutdated, "install")
	if err != nil {
		t.Error(err)
	}
	if !actionNeeded {
		t.Errorf("actionNeeded: %v; Expected checkPath to return true", actionNeeded)
	}
}

// captureConsole redirects gorillalog's console sink to a buffer (verbose so
// INFO-level status messages are visible) and restores it after the test.
func captureConsole(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	gorillalog.SetOutput(buf)
	t.Cleanup(func() {
		gorillalog.SetOutput(os.Stdout)
		gorillalog.Close()
	})
	if err := gorillalog.NewLog(cfgVerbose); err != nil {
		t.Fatalf("NewLog failed: %v", err)
	}
	return buf
}

// TestCheckStatusScript validates that a script check is ran
func TestCheckStatusScript(t *testing.T) {
	// Override execCommand with our fake version
	execCommand = fakeExecCommand
	defer func() { execCommand = origExec }()
	console := captureConsole(t)

	// Run CheckStatus with an item that has a script check
	_, _ = (&Checker{}).CheckStatus(scriptCheckItem, "install", "testdata/")

	if want := `msg="Checking status via script" item=scriptCheckItem`; !strings.Contains(console.String(), want) {
		t.Errorf("console output missing %q:\n%s", want, console.String())
	}
}

// TestCheckStatusFile validates that a file check is ran
func TestCheckStatusFile(t *testing.T) {
	// Override execCommand with our fake version
	execCommand = fakeExecCommand
	defer func() { execCommand = origExec }()
	console := captureConsole(t)

	// Run CheckStatus with an item that has a file check
	_, _ = (&Checker{}).CheckStatus(fileCheckItem, "install", "testdata/")

	if want := `msg="Checking status via file" item=fileCheckItem`; !strings.Contains(console.String(), want) {
		t.Errorf("console output missing %q:\n%s", want, console.String())
	}
}

// TestCheckStatusRegistry validates that a registry check is ran
func TestCheckStatusRegistry(t *testing.T) {
	// Override execCommand with our fake version
	execCommand = fakeExecCommand
	defer func() { execCommand = origExec }()
	console := captureConsole(t)

	// Run CheckStatus with an item that has a registry check
	_, _ = (&Checker{}).CheckStatus(registryCheckItem, "install", "testdata/")

	if want := `msg="Checking status via registry" item=registryCheckItem`; !strings.Contains(console.String(), want) {
		t.Errorf("console output missing %q:\n%s", want, console.String())
	}
}

// TestCheckStatusRegistryWithEmptyFileList guards catalogs built by the old
// `gorilla -build`, which wrote `file: []` on every item. An empty list is no
// file check, so the registry check must still run.
func TestCheckStatusRegistryWithEmptyFileList(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = origExec }()
	console := captureConsole(t)

	item := registryCheckItem
	item.Check.File = []catalog.FileCheck{}
	_, _ = (&Checker{}).CheckStatus(item, "install", "testdata/")

	if want := `msg="Checking status via registry" item=registryCheckItem`; !strings.Contains(console.String(), want) {
		t.Errorf("console output missing %q:\n%s", want, console.String())
	}
}

// TestCheckStatusAppx validates that an appx check is ran
func TestCheckStatusAppx(t *testing.T) {
	execCommand = fakeExecCommandAppx
	defer func() { execCommand = origExec }()
	console := captureConsole(t)

	_, _ = (&Checker{}).CheckStatus(appxCheckItem, "install", "testdata/")

	if want := `msg="Checking status via appx" item=appxCheckItem`; !strings.Contains(console.String(), want) {
		t.Errorf("console output missing %q:\n%s", want, console.String())
	}
}

// TestCheckStatusNone validates that no check is ran
func TestCheckStatusNone(t *testing.T) {
	// Override execCommand with our fake version
	execCommand = fakeExecCommand
	defer func() { execCommand = origExec }()
	console := captureConsole(t)

	// Run CheckStatus with an item that has no check data
	_, _ = (&Checker{}).CheckStatus(noCheckItem, "install", "testdata/")

	if want := `msg="Not enough data to check the current status" item=noCheckItem`; !strings.Contains(console.String(), want) {
		t.Errorf("console output missing %q:\n%s", want, console.String())
	}
}
