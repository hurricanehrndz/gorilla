package process

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/report"
)

var (
	// store original data to restore after each test
	origInstall  = installerInstall
	origOsRemove = osRemove

	// Setup a test catalog
	testCatalogs = map[int]map[string]catalog.Item{1: {
		"Chocolatey": catalog.Item{
			DisplayName: "Chocolatey",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "Chocolatey.msi",
			},
			Dependencies: []string{`TestUpdate1`},
		},
		"GoogleChrome": catalog.Item{
			DisplayName: "GoogleChrome",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "GoogleChrome.msi",
			},
		},
		"TestInstall1": catalog.Item{
			DisplayName: "TestInstall1",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "TestInstall1.msi",
			},
		},
		"TestInstall2": catalog.Item{
			DisplayName: "TestInstall2",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "TestInstall2.msi",
			},
		},
		"AdobeFlash": catalog.Item{
			DisplayName: "AdobeFlash",
			Uninstaller: catalog.InstallerItem{
				Type:     "msi",
				Location: "AdobeUninst.msi",
			},
		},
		"Chef Client": catalog.Item{
			DisplayName: "Chef Client",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "chef.msi",
			},
		},
		"CanonDrivers": catalog.Item{
			DisplayName: "CanonDrivers",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "TestInstall1.msi",
			},
		},
		"TestUninstall1": catalog.Item{
			DisplayName: "TestUninstall1",
			Uninstaller: catalog.InstallerItem{
				Type:     "ps1",
				Location: "TestUninst2.ps1",
			},
		},
		"TestUninstall2": catalog.Item{
			DisplayName: "TestUninstall2",
			Uninstaller: catalog.InstallerItem{
				Type:     "exe",
				Location: "TestUninst2.exe",
			},
		},
		"TestUpdate1": catalog.Item{
			DisplayName: "TestUpdate1",
			Installer: catalog.InstallerItem{
				Type:     "nupkg",
				Location: "TestUpdate1.nupkg",
			},
		},
		"TestUpdate2": catalog.Item{
			DisplayName: "TestUpdate2",
			Installer: catalog.InstallerItem{
				Type:     "ps1",
				Location: "TestUpdate2.ps1",
			},
		},
		"MissingInstallerType": catalog.Item{
			DisplayName: "MissingInstallerType",
			Installer: catalog.InstallerItem{
				Location: "MissingInstallerType.msi",
			},
		},
		"MissingInstallerLocation": catalog.Item{
			DisplayName: "MissingInstallerLocation",
			Installer: catalog.InstallerItem{
				Type: "msi",
			},
		},
		"TestMsixInstallOnly": catalog.Item{
			DisplayName: "TestMsixInstallOnly",
			Check: catalog.InstallCheck{
				Appx: catalog.AppxCheck{
					Name: "TestPublisher.TestApp",
				},
			},
			Installer: catalog.InstallerItem{
				Type:     "msix",
				Location: "TestApp.msix",
			},
		},
		"ChainA": catalog.Item{
			DisplayName: "ChainA",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "ChainA.msi",
			},
			Dependencies: []string{"ChainB"},
		},
		"ChainB": catalog.Item{
			DisplayName: "ChainB",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "ChainB.msi",
			},
			Dependencies: []string{"ChainC"},
		},
		"ChainC": catalog.Item{
			DisplayName: "ChainC",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "ChainC.msi",
			},
		},
		"DiamondA": catalog.Item{
			DisplayName: "DiamondA",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "DiamondA.msi",
			},
			Dependencies: []string{"DiamondB", "DiamondC"},
		},
		"DiamondB": catalog.Item{
			DisplayName: "DiamondB",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "DiamondB.msi",
			},
			Dependencies: []string{"DiamondD"},
		},
		"DiamondC": catalog.Item{
			DisplayName: "DiamondC",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "DiamondC.msi",
			},
			Dependencies: []string{"DiamondD"},
		},
		"DiamondD": catalog.Item{
			DisplayName: "DiamondD",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "DiamondD.msi",
			},
		},
		"CycleA": catalog.Item{
			DisplayName: "CycleA",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "CycleA.msi",
			},
			Dependencies: []string{"CycleB"},
		},
		"CycleB": catalog.Item{
			DisplayName: "CycleB",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "CycleB.msi",
			},
			Dependencies: []string{"CycleA"},
		},
		"NeedsFailing": catalog.Item{
			DisplayName: "NeedsFailing",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "NeedsFailing.msi",
			},
			Dependencies: []string{"FailingDep"},
		},
		"FailingDep": catalog.Item{
			DisplayName: "FailingDep",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "FailingDep.msi",
			},
		},
		"NeedsMissing": catalog.Item{
			DisplayName: "NeedsMissing",
			Installer: catalog.InstallerItem{
				Type:     "msi",
				Location: "NeedsMissing.msi",
			},
			Dependencies: []string{"MissingDep"},
		},
		"TestMsixUninstall": catalog.Item{
			DisplayName: "TestMsixUninstall",
			Check: catalog.InstallCheck{
				Appx: catalog.AppxCheck{
					Name: "TestPublisher.TestApp",
				},
			},
			Uninstaller: catalog.InstallerItem{
				Type: "msix",
			},
		},
	}}

	// A run context for tests; the install function itself is faked
	testRunner = &installer.Runner{URLPackages: "URLPackages", CachePath: "CachePath"}

	// Arrays of the test items
	testInstalls   = []string{"Chocolatey", "GoogleChrome", "TestInstall1", "TestInstall2"}
	testUninstalls = []string{"AdobeFlash", "TestUninstall1", "TestUninstall2"}
	testUpdates    = []string{"Chef Client", "CanonDrivers", "TestUpdate1", "TestUpdate2"}

	// Define a variable that our fake functions can store results in
	actualInstalledItems   []string
	actualUninstalledItems []string
	actualUpdatedItems     []string
	actualRemovedFiles     []string
)

// TestManifests verifies that the installs, uninstalls, and upgrades are processed correctly
func TestManifests(t *testing.T) {
	// Setup our test manifests
	testManifests := []manifest.Item{
		{
			Name:       "example_manifest",
			Includes:   []string{"included_manifest"},
			Installs:   []string{"Chocolatey", "GoogleChrome"},
			Uninstalls: []string{"AdobeFlash"},
			Updates:    []string{"Chef Client", "CanonDrivers"},
		},
		{
			Name:       "included_manifest",
			Includes:   []string(nil),
			Installs:   []string{"TestInstall1", "TestInstall2", "MissingInstallerType", "MissingInstallerLocation"},
			Uninstalls: []string{"TestUninstall1", "TestUninstall2"},
			Updates:    []string{"TestUpdate1", "TestUpdate2"},
		},
	}

	// Store the actual results of running `Manifests`
	actualInstalls, actualUninstalls, actualUpdates := Manifests(testManifests, testCatalogs)

	// Define what we expect it to return
	expectedInstalls := testInstalls
	expectedUninstalls := testUninstalls
	expectedUpdates := testUpdates

	// Compare our expectaions with the actual results
	matchInstalls := reflect.DeepEqual(expectedInstalls, actualInstalls)
	matchUninstalls := reflect.DeepEqual(expectedUninstalls, actualUninstalls)
	matchUpdates := reflect.DeepEqual(expectedUpdates, actualUpdates)

	// Fail if we dont match
	if !matchInstalls {
		t.Errorf("Manifest Installs\nExpected: %#v\nActual: %#v", expectedInstalls, actualInstalls)
	}
	if !matchUninstalls {
		t.Errorf("Manifest Uninstalls\nExpected: %#v\nActual: %#v", expectedUninstalls, actualUninstalls)
	}
	if !matchUpdates {
		t.Errorf("Manifest Updates\nExpected: %#v\nActual: %#v", expectedUpdates, actualUpdates)
	}
}

func TestFirstItemInvalidReturnsFalse(t *testing.T) {
	_, ok := firstItem("MissingInstallerType", testCatalogs)
	if ok {
		t.Fatalf("expected invalid catalog item to be skipped")
	}

	_, ok = firstItem("MissingInstallerLocation", testCatalogs)
	if ok {
		t.Fatalf("expected invalid catalog item to be skipped")
	}

	_, ok = firstItem("DoesNotExist", testCatalogs)
	if ok {
		t.Fatalf("expected missing catalog item to be skipped")
	}

	item, ok := firstItem("Chocolatey", testCatalogs)
	if !ok {
		t.Fatalf("expected valid catalog item")
	}
	if item.DisplayName != "Chocolatey" {
		t.Fatalf("unexpected item returned: %#v", item)
	}
}

// TestFirstItemMsixNoLocationIsValid verifies that an msix uninstall item with no
// location is considered valid (msix uninstalls use the package name, not a file).
func TestFirstItemMsixNoLocationIsValid(t *testing.T) {
	item, ok := firstItem("TestMsixUninstall", testCatalogs)
	if !ok {
		t.Fatalf("expected msix uninstall item with no location to be valid")
	}
	if item.DisplayName != "TestMsixUninstall" {
		t.Fatalf("unexpected item returned: %#v", item)
	}
}

// TestFirstItemMsixInstallerTypeIsValid verifies that an msix item with only an
// installer block (no explicit uninstaller) is still considered valid for uninstall,
// since msix uninstalls are handled via the package name rather than a file.
func TestFirstItemMsixInstallerTypeIsValid(t *testing.T) {
	item, ok := firstItem("TestMsixInstallOnly", testCatalogs)
	if !ok {
		t.Fatalf("expected msix installer-only item to be valid for uninstall")
	}
	if item.DisplayName != "TestMsixInstallOnly" {
		t.Fatalf("unexpected item returned: %#v", item)
	}
}

// TestUninstallsMsixInferredFromInstaller verifies that an msix item with no explicit
// uninstaller block is processed correctly when queued for uninstall.
func TestUninstallsMsixInferredFromInstaller(t *testing.T) {
	installerInstall = fakeUninstall
	defer func() {
		installerInstall = origInstall
		actualUninstalledItems = nil
	}()

	msixUninstalls := []string{"TestMsixInstallOnly"}
	Uninstalls(msixUninstalls, testCatalogs, testRunner)

	expectedItems := msixUninstalls
	matchItems := reflect.DeepEqual(expectedItems, actualUninstalledItems)
	if !matchItems {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedItems, actualUninstalledItems)
	}
}

// TestUninstallsMsix verifies that msix uninstall items are processed correctly.
func TestUninstallsMsix(t *testing.T) {
	installerInstall = fakeUninstall
	defer func() {
		installerInstall = origInstall
		actualUninstalledItems = nil
	}()

	msixUninstalls := []string{"TestMsixUninstall"}
	Uninstalls(msixUninstalls, testCatalogs, testRunner)

	expectedItems := msixUninstalls
	matchItems := reflect.DeepEqual(expectedItems, actualUninstalledItems)
	if !matchItems {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedItems, actualUninstalledItems)
	}
}

// TestInstalls tests if install items and their dependencies are processed correctly
func TestInstalls(t *testing.T) {
	// Override the install function to use our fake function
	installerInstall = fakeInstall
	defer func() { installerInstall = origInstall }()

	// Run `Installs` with test data
	Installs(testInstalls, testCatalogs, testRunner)

	// Define what we expect to be in the list of installed items
	// This ends up being the testInstalls slice *PLUS any dependencies*
	expectedItems := append([]string{"TestUpdate1"}, testInstalls...)

	// Compare our expectaions with the actual results
	matchItems := reflect.DeepEqual(expectedItems, actualInstalledItems)

	// Fail if we dont match
	if !matchItems {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedItems, actualInstalledItems)
	}
}

// TestInstallsContinuePastFailure verifies Munki semantics: an item failure
// is logged and the run continues to the remaining items (K1), while a
// dependent of a failed dependency is skipped and recorded (K4)
func TestInstallsContinuePastFailure(t *testing.T) {
	// Override the install function with one that always fails
	installerInstall = func(r *installer.Runner, item catalog.Item, installerType string) (string, error) {
		actualInstalledItems = append(actualInstalledItems, item.DisplayName)
		return "", errors.New("action failed")
	}
	actualInstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	}()

	// Run `Installs` with test data
	r := &installer.Runner{Report: report.New()}
	Installs(testInstalls, testCatalogs, r)

	// Every independent item must still have been attempted despite the
	// failures; Chocolatey is skipped because its dependency TestUpdate1 failed
	expectedItems := []string{"TestUpdate1", "GoogleChrome", "TestInstall1", "TestInstall2"}
	if !reflect.DeepEqual(expectedItems, actualInstalledItems) {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedItems, actualInstalledItems)
	}
	assertFailedItems(t, r, map[string]string{"Chocolatey": "dependency TestUpdate1 failed"})
}

// assertFailedItems checks that the report contains exactly the expected
// failed items, each recorded once with the expected error
func assertFailedItems(t *testing.T, r *installer.Runner, expected map[string]string) {
	t.Helper()
	actual := make(map[string]string)
	for _, failed := range r.Report.FailedItems {
		if _, dup := actual[failed.Name]; dup {
			t.Errorf("item recorded in FailedItems more than once: %v", failed.Name)
		}
		actual[failed.Name] = failed.Error
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("FailedItems\nExpected: %#v\nActual: %#v", expected, actual)
	}
}

// TestInstallsDependencyChain verifies that transitive dependencies install
// depth-first before their dependents (K4)
func TestInstallsDependencyChain(t *testing.T) {
	installerInstall = fakeInstall
	actualInstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	}()

	r := &installer.Runner{Report: report.New()}
	Installs([]string{"ChainA"}, testCatalogs, r)

	expectedItems := []string{"ChainC", "ChainB", "ChainA"}
	if !reflect.DeepEqual(expectedItems, actualInstalledItems) {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedItems, actualInstalledItems)
	}
	assertFailedItems(t, r, map[string]string{})
}

// TestInstallsSharedDependency verifies that a dependency shared by two
// dependents is only processed once per run (K4)
func TestInstallsSharedDependency(t *testing.T) {
	installerInstall = fakeInstall
	actualInstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	}()

	r := &installer.Runner{Report: report.New()}
	Installs([]string{"DiamondA"}, testCatalogs, r)

	expectedItems := []string{"DiamondD", "DiamondB", "DiamondC", "DiamondA"}
	if !reflect.DeepEqual(expectedItems, actualInstalledItems) {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedItems, actualInstalledItems)
	}
	assertFailedItems(t, r, map[string]string{})
}

// TestInstallsDependencyCycle verifies that a dependency cycle is detected,
// the cycled items are recorded as failed and skipped, and the run continues
// to other items (K4)
func TestInstallsDependencyCycle(t *testing.T) {
	installerInstall = fakeInstall
	actualInstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	}()

	r := &installer.Runner{Report: report.New()}
	Installs([]string{"CycleA", "GoogleChrome"}, testCatalogs, r)

	// Neither cycled item installs; the run continues to GoogleChrome
	expectedItems := []string{"GoogleChrome"}
	if !reflect.DeepEqual(expectedItems, actualInstalledItems) {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedItems, actualInstalledItems)
	}
	assertFailedItems(t, r, map[string]string{
		"CycleA": "dependency cycle detected",
		"CycleB": "dependency CycleA failed",
	})
}

// TestInstallsFailedDependencyBlocksDependent verifies that a dependency whose
// install fails causes its dependent to be skipped and recorded, while
// independent items are still processed (K4)
func TestInstallsFailedDependencyBlocksDependent(t *testing.T) {
	installerInstall = func(r *installer.Runner, item catalog.Item, installerType string) (string, error) {
		actualInstalledItems = append(actualInstalledItems, item.DisplayName)
		if item.DisplayName == "FailingDep" {
			return "", errors.New("action failed")
		}
		return "", nil
	}
	actualInstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	}()

	r := &installer.Runner{Report: report.New()}
	Installs([]string{"NeedsFailing", "GoogleChrome"}, testCatalogs, r)

	// FailingDep is attempted; NeedsFailing is skipped; GoogleChrome still runs
	expectedItems := []string{"FailingDep", "GoogleChrome"}
	if !reflect.DeepEqual(expectedItems, actualInstalledItems) {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedItems, actualInstalledItems)
	}
	// Only the skipped dependent is recorded here; the real installer records
	// the failing item itself
	assertFailedItems(t, r, map[string]string{"NeedsFailing": "dependency FailingDep failed"})
}

// TestInstallsMissingDependencyBlocksDependent verifies that a dependency
// missing from all catalogs is recorded as failed and its dependent is
// skipped and recorded (K4)
func TestInstallsMissingDependencyBlocksDependent(t *testing.T) {
	installerInstall = fakeInstall
	actualInstalledItems = nil
	defer func() {
		installerInstall = origInstall
		actualInstalledItems = nil
	}()

	r := &installer.Runner{Report: report.New()}
	Installs([]string{"NeedsMissing"}, testCatalogs, r)

	if len(actualInstalledItems) != 0 {
		t.Errorf("\nExpected no installs\nActual: %#v", actualInstalledItems)
	}
	assertFailedItems(t, r, map[string]string{
		"MissingDep":   "not found in any catalog",
		"NeedsMissing": "dependency MissingDep failed",
	})
}

// TestUninstalls tests if uninstall items are processed correctly
func TestUninstalls(t *testing.T) {
	// Override the install function to use our fake function
	installerInstall = fakeUninstall
	defer func() { installerInstall = origInstall }()

	// Run `Uninstalls` with test data
	Uninstalls(testUninstalls, testCatalogs, testRunner)

	// Define what we expect to be in the list of uninstalled items
	expectedItems := testUninstalls

	// Compare our expectaions with the actual results
	matchItems := reflect.DeepEqual(expectedItems, actualUninstalledItems)

	// Fail if we dont match
	if !matchItems {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedItems, actualUninstalledItems)
	}
}

// TestUpdates tests if update items are processed correctly
func TestUpdates(t *testing.T) {
	// Override the install function to use our fake function
	installerInstall = fakeUpdate
	defer func() { installerInstall = origInstall }()

	// Run `Updates` with test data
	Updates(testUpdates, testCatalogs, testRunner)

	// Define what we expect to be in the list of updated items
	expectedItems := testUpdates

	// Compare our expectaions with the actual results
	matchItems := reflect.DeepEqual(expectedItems, actualUpdatedItems)

	// Fail if we dont match
	if !matchItems {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedItems, actualUpdatedItems)
	}
}

// TestCleanUp verifies that only the correct files and directories are removed
func TestCleanUp(t *testing.T) {
	// Override the os.Remove function
	osRemove = fakeOsRemove
	defer func() {
		osRemove = origOsRemove
	}()

	// Define new and old times
	newTime := time.Now().Add(-24 * time.Hour)  // 1 day
	oldTime := time.Now().Add(-240 * time.Hour) // 10 days

	// Define the various file paths we will user
	emptyDir := filepath.Clean("testdata/cache/empty")
	oldFile := filepath.Clean("testdata/cache/old.msi")
	newFile := filepath.Clean("testdata/cache/new.msi")
	childFile := filepath.Clean("testdata/cache/full/file.msi")

	// Set the timestamps on each test file
	err := os.Chtimes(oldFile, oldTime, oldTime)
	if err != nil {
		t.Error(err)
	}
	err = os.Chtimes(newFile, newTime, newTime)
	if err != nil {
		t.Error(err)
	}
	err = os.Chtimes(childFile, newTime, newTime)
	if err != nil {
		t.Error(err)
	}

	// Create an empty directory if it doesn't already exist
	if _, err := os.Stat(emptyDir); os.IsNotExist(err) {
		// Directory does not exist
		os.Mkdir(emptyDir, os.ModePerm)
	}

	// Run `CleanUp`
	CleanUp("testdata/")

	// Define the files and directories we expect to be deleted
	expectedFiles := []string{oldFile, emptyDir}

	// Compare our expectaions with the actual results
	matchItems := reflect.DeepEqual(expectedFiles, actualRemovedFiles)

	// Fail if we dont match
	if !matchItems {
		t.Errorf("\nExpected: %#v\nActual: %#v", expectedFiles, actualRemovedFiles)
	}
}

// Mocks the actual `installer.Install` function and saves what it receives to `actualInstalledItems`
func fakeInstall(r *installer.Runner, item catalog.Item, installerType string) (string, error) {
	// Append any item we are passed to a slice for later comparison
	actualInstalledItems = append(actualInstalledItems, item.DisplayName)
	return "", nil
}

// Mocks the actual `installer.Install` function and saves what it receives to `actualUninstalledItems`
func fakeUninstall(r *installer.Runner, item catalog.Item, installerType string) (string, error) {
	// Append any item we are passed to a slice for later comparison
	actualUninstalledItems = append(actualUninstalledItems, item.DisplayName)
	return "", nil
}

// Mocks the actual `installer.Install` function and saves what it receives to `actualUpdatedItems`
func fakeUpdate(r *installer.Runner, item catalog.Item, installerType string) (string, error) {
	// Append any item we are passed to a slice for later comparison
	actualUpdatedItems = append(actualUpdatedItems, item.DisplayName)
	return "", nil
}

// Mock `os.Remove` so we dont delete files during testing
func fakeOsRemove(name string) error {
	actualRemovedFiles = append(actualRemovedFiles, name)
	return nil
}
