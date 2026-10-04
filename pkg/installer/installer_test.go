package installer

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/download"
	"github.com/1dustindavis/gorilla/pkg/gorillalog"
	"github.com/1dustindavis/gorilla/pkg/report"
	"github.com/1dustindavis/gorilla/pkg/status"
)

// A lot of ideas taken from https://npf.io/2015/06/testing-exec-command/

var (
	// store original data to restore after each test
	origExec            = execCommand
	origCheckStatus     = statusCheckStatus
	origInstallItemFunc = installItemFunc
	origRunCommand      = runCommand

	// These tore the URL that `Install` generates during testing
	installItemURL   string
	uninstallItemURL string

	// Define a testing config for `download`
	downloadCfg = config.Configuration{
		CachePath: "testdata/",
	}

	// These catalog items provide test data for each installer type
	nupkgItem = catalog.Item{
		Installer: catalog.InstallerItem{
			Arguments: []string{`/L=1033`, `/S`},
			Hash:      `f441893d760c411c25420a0cb4ba3a2c708fa69d7ed455818bef1a5fd4ae7577`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64.nupkg`,
			Type:      `nupkg`,
		},
		Uninstaller: catalog.InstallerItem{
			Arguments: []string{`/U=1033`, `/S`},
			Hash:      `a3fb64e1cadce0fd5bd08a7b01ce991c8c8bfb5618fa7e0975b6a7387dc26cba`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64uninst.nupkg`,
			Type:      `nupkg`,
		},
		Version: "1.2.3",
	}
	msiItem = catalog.Item{
		Installer: catalog.InstallerItem{
			Arguments: []string{`/L=1033`, `/S`},
			Hash:      `a1d4982abbb2bd2ccc238372ae688c790659c2c120efcee329fcca49c7c8fa9a`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64.msi`,
			Type:      `msi`,
		},
		Uninstaller: catalog.InstallerItem{
			Arguments: []string{`/U=1033`, `/S`},
			Hash:      `069068fea26346a7c006f39f8d84ced2ebb6b874a35143f52ed979d29f11ef3d`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64uninst.msi`,
			Type:      `msi`,
		},
		Version: "1.2.3",
	}
	exeItem = catalog.Item{
		Installer: catalog.InstallerItem{
			Arguments: []string{`/L=1033`, `/S`},
			Hash:      `7235428c924193a353db253c59cfbf1501299df6fefcb23fa577ea96612473da`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64.exe`,
			Type:      `exe`,
		},
		Uninstaller: catalog.InstallerItem{
			Arguments: []string{`/U=1033`, `/S`},
			Hash:      `9dc6a2c1c1ae2c3f399d7ac3c01eb5ac2976e55e8bedb842755eebe3b9add9e7`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64uninst.exe`,
			Type:      `exe`,
		},
	}
	ps1Item = catalog.Item{
		Installer: catalog.InstallerItem{
			Arguments: []string{`/L=1033`, `/S`},
			Hash:      `195f5d4d521ca39f96b7d8fd5edd96d1f129493ddb56ae1c5c6db6cefe2167ee`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64.ps1`,
			Type:      `ps1`,
		},
		Uninstaller: catalog.InstallerItem{
			Arguments: []string{`/U=1033`, `/S`},
			Hash:      `0c6f40ae30bcf5e3658bef5122037c927b72bc5a6e0bbf48d7294a0e453d620e`,
			Location:  `packages/chef-client/chef-client-14.3.37-1-x64uninst.ps1`,
			Type:      `ps1`,
		},
	}
	msixItem = catalog.Item{
		Installer: catalog.InstallerItem{
			Hash:     `fcc18ed417b62314901e2712933f89e55d5900f2c3cf883100e7fcef1ef1de74`,
			Location: `packages/chef-client/chef-client-14.3.37-1-x64.msix`,
			Type:     `msix`,
		},
		Uninstaller: catalog.InstallerItem{
			Type: `msix`,
		},
		Check: catalog.InstallCheck{
			Appx: catalog.AppxCheck{
				Name: `Gorilla.Test.App`,
			},
		},
		Version: "1.0.0",
	}

	// Define different options to bypass status checks during tests
	statusActionNoError   = `_gorilla_dev_action_noerror_`
	statusNoActionNoError = `_gorilla_dev_noaction_noerror_`
	statusActionError     = `_gorilla_dev_action_error_`
	statusNoActionError   = `_gorilla_dev_noaction_error_`
)

// fakeExecCommand provides a method for validating what is passed to exec.Command
// this function was copied verbatim from https://npf.io/2015/06/testing-exec-command/
func fakeExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	return cmd
}

// fakeExecCommandFail is fakeExecCommand except the helper process exits 1
func fakeExecCommandFail(command string, args ...string) *exec.Cmd {
	cmd := fakeExecCommand(command, args...)
	cmd.Env = append(cmd.Env, "GO_HELPER_PROCESS_FAIL=1")
	return cmd
}

// fakeRunCommand just returns a string and error interface
func fakeRunCommand(command string, arguments []string) (string, error) {
	cmdOutput := "This is a fake test command return"
	var err error
	if msiItem.DisplayName == statusActionNoError {
		err = nil
	} else if msiItem.DisplayName == statusActionError {
		err = fmt.Errorf("Deliberate test error has occurred!!")
	}

	return cmdOutput, err
}

// TestHelperProcess processes the commands passed to fakeExecCommand
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	// print the command we received
	fmt.Print(os.Args[3:])
	if os.Getenv("GO_HELPER_PROCESS_FAIL") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func fakeCheckStatus(_ *status.Checker, catalogItem catalog.Item, installType string, cachePath string) (install bool, checkErr error) {
	// Catch special names used in tests
	if catalogItem.DisplayName == statusActionNoError {
		slog.Warn("Running Development Tests!", "item", catalogItem.DisplayName)
		return true, nil
	} else if catalogItem.DisplayName == statusNoActionNoError {
		slog.Warn("Running Development Tests!", "item", catalogItem.DisplayName)
		return false, nil
	} else if catalogItem.DisplayName == statusActionError {
		slog.Warn("Running Development Tests!", "item", catalogItem.DisplayName)
		return true, fmt.Errorf("testing %v", catalogItem.DisplayName)
	} else if catalogItem.DisplayName == statusNoActionError {
		slog.Warn("Running Development Tests!", "item", catalogItem.DisplayName)
		return false, fmt.Errorf("testing %v", catalogItem.DisplayName)
	}

	fmt.Println(catalogItem.DisplayName)
	fmt.Println(installType)
	return false, nil
}

// newTestRunner returns a fresh run-scoped Runner wired for tests (K7: state
// is per-run, so tests no longer share or reset package globals).
func newTestRunner() *Runner {
	return &Runner{
		Report:      report.New(),
		Checker:     &status.Checker{},
		URLPackages: "https://example.com/",
		CachePath:   "testdata/",
	}
}

// TestRunCommand verifies that the command and it's arguments are processed correctly
func TestRunCommand(t *testing.T) {
	// Override execCommand with our fake version
	execCommand = fakeExecCommand
	defer func() { execCommand = origExec }()

	// Define our test command and arguments
	testCommand := "echo"
	testArgs := []string{"pizza", "sushi"}
	testCmd := append([]string{testCommand}, testArgs...)
	expectedCmd := fmt.Sprint(testCmd)

	actualCmd, _ := runCommand(testCommand, testArgs)

	// Compare the result with our expectations
	structsMatch := reflect.DeepEqual(expectedCmd, actualCmd)

	if !structsMatch {
		t.Errorf("\nExpected: %#v\nReceived: %#v", expectedCmd, actualCmd)
	}
}

// TestInstallItem validate the command that is passed to
// exec.Command for each installer type
func TestInstallItem(t *testing.T) {
	// Override execCommand and checkStatus with our fake versions
	execCommand = fakeExecCommand
	statusCheckStatus = fakeCheckStatus
	defer func() {
		execCommand = origExec
		statusCheckStatus = origCheckStatus
	}()
	r := newTestRunner()

	// Set shared testing variables
	pkgCache := "testdata/packages/"
	urlPackages := "https://example.com/"

	//
	// Nupkg
	//
	nupkgItem.DisplayName = statusActionNoError
	nupkgPath := "chef-client/chef-client-14.3.37-1-x64.nupkg"
	nupkgURL := urlPackages + nupkgPath

	// Run Install
	actualNupkg, nupkgErr := r.installItem(nupkgItem, nupkgURL)
	if nupkgErr != nil {
		t.Errorf("installItem nupkg returned an error: %v", nupkgErr)
	}

	// Check the result
	nupkgCmd := filepath.Join(os.Getenv("ProgramData"), "chocolatey/bin/choco.exe")
	nupkgFile := filepath.Join(pkgCache, nupkgPath)
	nupkgDir := filepath.Dir(nupkgFile)
	nupkgID := fmt.Sprintf("[%s list --version=1.2.3 --id-only -r -s %s]", nupkgCmd, nupkgDir)
	expectedNupkg := fmt.Sprintf("[%s install %s -s %s --version=1.2.3 -f -y -r]", nupkgCmd, nupkgID, nupkgDir)
	if have, want := actualNupkg, expectedNupkg; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}

	//
	// Msi
	//
	msiItem.DisplayName = statusActionNoError
	msiPath := "chef-client/chef-client-14.3.37-1-x64.msi"
	msiURL := urlPackages + msiPath

	// Run Install
	actualMsi, msiErr := r.installItem(msiItem, msiURL)
	if msiErr != nil {
		t.Errorf("installItem msi returned an error: %v", msiErr)
	}

	// Check the result
	msiCmd := filepath.Join(os.Getenv("WINDIR"), "system32/msiexec.exe")
	msiFile := filepath.Join(pkgCache, msiPath)
	expectedMsi := "[" + msiCmd + " /i " + msiFile + " /qn /norestart /L=1033 /S]"
	if have, want := actualMsi, expectedMsi; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}

	//
	// Exe
	//
	exeItem.DisplayName = statusActionNoError
	exePath := "chef-client/chef-client-14.3.37-1-x64.exe"
	exeURL := urlPackages + exePath

	// Run Install
	actualExe, exeErr := r.installItem(exeItem, exeURL)
	if exeErr != nil {
		t.Errorf("installItem exe returned an error: %v", exeErr)
	}

	// Check the result
	exeFile := filepath.Join(pkgCache, exePath)
	expectedExe := "[" + exeFile + " /L=1033 /S]"
	if have, want := actualExe, expectedExe; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}

	//
	// Ps1
	//
	ps1Item.DisplayName = statusActionNoError
	ps1Path := "chef-client/chef-client-14.3.37-1-x64.ps1"
	ps1URL := urlPackages + ps1Path

	// Run Install
	actualPs1, ps1Err := r.installItem(ps1Item, ps1URL)
	if ps1Err != nil {
		t.Errorf("installItem ps1 returned an error: %v", ps1Err)
	}

	// Check the result
	ps1Cmd := filepath.Join(os.Getenv("WINDIR"), "system32/WindowsPowershell/v1.0/powershell.exe")
	ps1File := filepath.Join(pkgCache, ps1Path)
	expectedPs1 := "[" + ps1Cmd + " -NoProfile -NoLogo -NonInteractive -ExecutionPolicy Bypass -File " + ps1File + "]"
	if have, want := actualPs1, expectedPs1; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}

	//
	// Msix
	//
	msixItem.DisplayName = statusActionNoError
	msixPath := "chef-client/chef-client-14.3.37-1-x64.msix"
	msixURL := urlPackages + msixPath

	// Run Install
	actualMsix, msixErr := r.installItem(msixItem, msixURL)
	if msixErr != nil {
		t.Errorf("installItem msix returned an error: %v", msixErr)
	}

	// Check the result
	msixCmd := filepath.Join(os.Getenv("WINDIR"), "system32/WindowsPowershell/v1.0/powershell.exe")
	msixFile := filepath.Join(pkgCache, msixPath)
	expectedMsix := "[" + msixCmd + " -NoProfile -NoLogo -NonInteractive -ExecutionPolicy Bypass -Command Add-AppxProvisionedPackage -Online -PackagePath '" + msixFile + "' -SkipLicense]"
	if have, want := actualMsix, expectedMsix; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
}

// TestInstallItemUnsupportedType verifies an unknown installer type is an
// error and lands in FailedItems (K1)
func TestInstallItemUnsupportedType(t *testing.T) {
	r := newTestRunner()

	item := msiItem
	item.DisplayName = "Unsupported Item"
	item.Name = "Unsupported Item"
	item.Installer.Type = "flatpak"

	out, err := r.installItem(item, "https://example.com/")
	if err == nil {
		t.Fatalf("expected an error for unsupported installer type, got output: %q", out)
	}
	if !strings.Contains(err.Error(), "unsupported installer type") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(r.Report.InstalledItems) != 0 {
		t.Errorf("unsupported item must not be reported as installed: %#v", r.Report.InstalledItems)
	}
	if len(r.Report.FailedItems) != 1 {
		t.Fatalf("expected 1 failed item, got %#v", r.Report.FailedItems)
	}
	if have := r.Report.FailedItems[0]; have.Name != "Unsupported Item" || have.Action != "install" || have.Error == "" {
		t.Errorf("unexpected failed item entry: %#v", have)
	}
}

// TestInstallStatusError verifies that Install returns an error if the status check fails
func TestInstallStatusError(t *testing.T) {
	// Override checkStatus with our fake version
	statusCheckStatus = fakeCheckStatus
	defer func() {
		statusCheckStatus = origCheckStatus
	}()

	// Run the msi installer with this status bypass to trigger an error
	msiItem.DisplayName = statusActionError
	msiItem.Name = statusActionError
	// Run Install
	r := newTestRunner()
	_, err := r.Install(msiItem, "install")
	// Check the result
	if err == nil {
		t.Fatalf("expected a status check error")
	}
	expectedOutput := "unable to check status: testing _gorilla_dev_action_error_"
	if have, want := err.Error(), expectedOutput; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
	// The failure must also land in the report
	if len(r.Report.FailedItems) != 1 {
		t.Fatalf("expected 1 failed item, got %#v", r.Report.FailedItems)
	}
	if have := r.Report.FailedItems[0]; have.Name != statusActionError || !strings.Contains(have.Error, "unable to check status") {
		t.Errorf("unexpected failed item entry: %#v", have)
	}
}

// TestInstallStatusFalse verifies that Install returns if status check is false
func TestInstallStatusFalse(t *testing.T) {
	// Override checkStatus with our fake version
	statusCheckStatus = fakeCheckStatus
	defer func() {
		statusCheckStatus = origCheckStatus
	}()

	// Run the msi installer with this status bypass to make status return false
	msiItem.DisplayName = statusNoActionNoError
	// Run Install
	runner := newTestRunner()
	actualOutput, err := runner.Install(msiItem, "install")
	if err != nil {
		t.Errorf("Install returned an error: %v", err)
	}
	// Check the result
	expectedOutput := "Item not needed"
	if have, want := actualOutput, expectedOutput; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
	// The inventory relies on this record to report the item as installed
	want := []report.NoActionItem{{Name: msiItem.Name, Version: msiItem.Version, Action: "install"}}
	if !reflect.DeepEqual(runner.Report.NoActionItems, want) {
		t.Errorf("NoActionItems = %#v, want %#v", runner.Report.NoActionItems, want)
	}
	if len(runner.Report.InstalledItems) != 0 {
		t.Errorf("no-action item recorded as installed this run: %#v", runner.Report.InstalledItems)
	}
}

// TestInstallCheckOnly verifies that check-only mode performs no action,
// returns its usual output, and records the pending item in the run report
func TestInstallCheckOnly(t *testing.T) {
	// Override checkStatus with our fake version
	statusCheckStatus = fakeCheckStatus
	defer func() {
		statusCheckStatus = origCheckStatus
	}()
	r := newTestRunner()
	r.CheckOnly = true

	// Run the msi installer with this status bypass to make status return true
	msiItem.DisplayName = statusActionNoError
	// Run Install
	actualOutput, err := r.Install(msiItem, "install")
	if err != nil {
		t.Errorf("Install returned an error: %v", err)
	}
	// Check the result
	expectedOutput := "Check only enabled"
	if have, want := actualOutput, expectedOutput; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
	if len(r.Report.InstalledItems) != 1 {
		t.Errorf("check-only run must record the pending item: %#v", r.Report.InstalledItems)
	}
}

// TestInstallUnsupportedItemType verifies that Install errors on an unknown action
func TestInstallUnsupportedItemType(t *testing.T) {
	// Override checkStatus with our fake version
	statusCheckStatus = fakeCheckStatus
	defer func() {
		statusCheckStatus = origCheckStatus
	}()

	msiItem.DisplayName = statusActionNoError
	_, err := newTestRunner().Install(msiItem, "sideload")
	if err == nil || !strings.Contains(err.Error(), "unsupported item type") {
		t.Errorf("expected unsupported item type error, got: %v", err)
	}
}

// TestUninstallItem validate the command that is passed to
// exec.Command for each installer type
func TestUninstallItem(t *testing.T) {
	// Override execCommand and checkStatus with our fake versions
	execCommand = fakeExecCommand
	statusCheckStatus = fakeCheckStatus
	download.SetConfig(downloadCfg)
	defer func() {
		execCommand = origExec
		statusCheckStatus = origCheckStatus
	}()
	r := newTestRunner()

	// Set shared testing variables
	pkgCache := "testdata/packages/"
	urlPackages := "https://example.com/"

	//
	// Nupkg
	//
	nupkgItem.DisplayName = statusNoActionNoError
	nupkgPath := "chef-client/chef-client-14.3.37-1-x64uninst.nupkg"
	nupkgURL := urlPackages + nupkgPath
	// Run Uninstall
	actualNupkg, nupkgErr := r.uninstallItem(nupkgItem, nupkgURL)
	if nupkgErr != nil {
		t.Errorf("uninstallItem nupkg returned an error: %v", nupkgErr)
	}
	// Check the result
	nupkgCmd := filepath.Join(os.Getenv("ProgramData"), "chocolatey/bin/choco.exe")
	nupkgFile := filepath.Join(pkgCache, nupkgPath)
	nupkgDir := filepath.Dir(nupkgFile)
	nupkgID := fmt.Sprintf("[%s list --version=1.2.3 --id-only -r -s %s]", nupkgCmd, nupkgDir)
	expectedNupkg := fmt.Sprintf("[%s uninstall %s -s %s --version=1.2.3 -f -y -r]", nupkgCmd, nupkgID, nupkgDir)
	if have, want := actualNupkg, expectedNupkg; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}

	//
	// Msi
	//
	msiItem.DisplayName = statusNoActionNoError
	// Run Uninstall
	actualMsi, msiErr := r.uninstallItem(msiItem, urlPackages)
	if msiErr != nil {
		t.Errorf("uninstallItem msi returned an error: %v", msiErr)
	}
	// Check the result
	msiCmd := filepath.Join(os.Getenv("WINDIR"), "system32/msiexec.exe")
	msiPath := filepath.Clean("testdata/packages/chef-client/chef-client-14.3.37-1-x64uninst.msi")
	expectedMsi := "[" + msiCmd + " /x " + msiPath + " /qn /norestart]"
	if have, want := actualMsi, expectedMsi; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}

	//
	// Exe
	//
	exeItem.DisplayName = statusNoActionNoError
	// Run Uninstall
	actualExe, exeErr := r.uninstallItem(exeItem, urlPackages)
	if exeErr != nil {
		t.Errorf("uninstallItem exe returned an error: %v", exeErr)
	}
	// Check the result
	exePath := filepath.Clean("testdata/packages/chef-client/chef-client-14.3.37-1-x64uninst.exe")
	expectedExe := "[" + exePath + " /U=1033 /S]"
	if have, want := actualExe, expectedExe; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}

	//
	// Ps1
	//
	ps1Item.DisplayName = statusNoActionNoError
	// Run Uninstall
	actualPs1, ps1Err := r.uninstallItem(ps1Item, urlPackages)
	if ps1Err != nil {
		t.Errorf("uninstallItem ps1 returned an error: %v", ps1Err)
	}
	// Check the result
	ps1Cmd := filepath.Join(os.Getenv("WINDIR"), "system32/WindowsPowershell/v1.0/powershell.exe")
	ps1Path := filepath.Clean("testdata/packages/chef-client/chef-client-14.3.37-1-x64uninst.ps1")
	expectedPs1 := "[" + ps1Cmd + " -NoProfile -NoLogo -NonInteractive -ExecutionPolicy Bypass -File " + ps1Path + "]"
	if have, want := actualPs1, expectedPs1; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}

	//
	// Msix
	//
	msixItem.DisplayName = statusNoActionNoError
	// Run Uninstall (msix uses Check.Appx.Name, no file download needed)
	actualMsix, msixErr := r.uninstallItem(msixItem, "")
	if msixErr != nil {
		t.Errorf("uninstallItem msix returned an error: %v", msixErr)
	}
	// Check the result
	msixCmd := filepath.Join(os.Getenv("WINDIR"), "system32/WindowsPowershell/v1.0/powershell.exe")
	expectedMsix := "[" + msixCmd + " -NoProfile -NoLogo -NonInteractive -ExecutionPolicy Bypass -Command $pkg = Get-AppxProvisionedPackage -Online | Where-Object { $_.DisplayName -eq 'Gorilla.Test.App' }; if ($pkg) { Remove-AppxProvisionedPackage -Online -PackageName $pkg.PackageName }; Get-AppxPackage -Name 'Gorilla.Test.App' -AllUsers | Remove-AppxPackage -AllUsers -ErrorAction SilentlyContinue]"
	if have, want := actualMsix, expectedMsix; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
}

func TestInstallItemNupkgWithExplicitPackageID(t *testing.T) {
	execCommand = fakeExecCommand
	runCommand = origRunCommand
	defer func() {
		execCommand = origExec
		runCommand = origRunCommand
	}()
	r := newTestRunner()

	pkgCache := "testdata/packages/"
	urlPackages := "https://example.com/"

	nupkgPath := "chef-client/chef-client-14.3.37-1-x64.nupkg"
	nupkgURL := urlPackages + nupkgPath
	nupkgFile := filepath.Join(pkgCache, nupkgPath)
	nupkgDir := filepath.Dir(nupkgFile)

	item := nupkgItem
	item.DisplayName = statusActionNoError
	item.Installer.PackageID = "chef-client"

	actual, err := r.installItem(item, nupkgURL)
	if err != nil {
		t.Errorf("installItem returned an error: %v", err)
	}
	expected := fmt.Sprintf("[%s install chef-client -s %s --version=1.2.3 -f -y -r]", commandNupkg, nupkgDir)

	if have, want := actual, expected; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
}

func TestInstallItemNupkgAmbiguousPackageID(t *testing.T) {
	runCommand = func(command string, arguments []string) (string, error) {
		if len(arguments) > 0 && arguments[0] == "list" {
			return "chef-client\nchef-client-alt", nil
		}
		t.Fatalf("unexpected install command was executed: %s %s", command, strings.Join(arguments, " "))
		return "", nil
	}
	defer func() { runCommand = origRunCommand }()
	r := newTestRunner()

	urlPackages := "https://example.com/"
	nupkgPath := "chef-client/chef-client-14.3.37-1-x64.nupkg"
	nupkgURL := urlPackages + nupkgPath

	item := nupkgItem
	item.DisplayName = "Ambiguous Package"
	item.Installer.PackageID = ""

	_, err := r.installItem(item, nupkgURL)
	if err == nil {
		t.Fatalf("expected an id-resolution error")
	}
	if !strings.Contains(err.Error(), "unable to determine nupkg id") || !strings.Contains(err.Error(), "multiple package ids were found") {
		t.Fatalf("expected ambiguity error, got: %v", err)
	}
	// id-resolution failure is an honest failure (K1)
	if len(r.Report.FailedItems) != 1 {
		t.Fatalf("expected 1 failed item, got %#v", r.Report.FailedItems)
	}
}

func TestUninstallItemNupkgWithExplicitPackageID(t *testing.T) {
	execCommand = fakeExecCommand
	runCommand = origRunCommand
	defer func() {
		execCommand = origExec
		runCommand = origRunCommand
	}()
	r := newTestRunner()

	pkgCache := "testdata/packages/"
	urlPackages := "https://example.com/"

	nupkgPath := "chef-client/chef-client-14.3.37-1-x64uninst.nupkg"
	nupkgURL := urlPackages + nupkgPath
	nupkgFile := filepath.Join(pkgCache, nupkgPath)
	nupkgDir := filepath.Dir(nupkgFile)

	item := nupkgItem
	item.DisplayName = statusNoActionNoError
	item.Uninstaller.PackageID = "chef-client"

	actual, err := r.uninstallItem(item, nupkgURL)
	if err != nil {
		t.Errorf("uninstallItem returned an error: %v", err)
	}
	expected := fmt.Sprintf("[%s uninstall chef-client -s %s --version=1.2.3 -f -y -r]", commandNupkg, nupkgDir)

	if have, want := actual, expected; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
}

func TestUninstallItemNupkgAmbiguousPackageID(t *testing.T) {
	runCommand = func(command string, arguments []string) (string, error) {
		if len(arguments) > 0 && arguments[0] == "list" {
			return "chef-client\nchef-client-alt", nil
		}
		t.Fatalf("unexpected uninstall command was executed: %s %s", command, strings.Join(arguments, " "))
		return "", nil
	}
	defer func() { runCommand = origRunCommand }()
	r := newTestRunner()

	urlPackages := "https://example.com/"
	nupkgPath := "chef-client/chef-client-14.3.37-1-x64uninst.nupkg"
	nupkgURL := urlPackages + nupkgPath

	item := nupkgItem
	item.DisplayName = "Ambiguous Uninstall Package"
	item.Uninstaller.PackageID = ""

	_, err := r.uninstallItem(item, nupkgURL)
	if err == nil {
		t.Fatalf("expected an id-resolution error")
	}
	if !strings.Contains(err.Error(), "unable to determine nupkg id") || !strings.Contains(err.Error(), "multiple package ids were found") {
		t.Fatalf("expected ambiguity error, got: %v", err)
	}
}

// TestUninstallItemMsixMissingName verifies that uninstall returns an error when Check.Appx.Name is empty
func TestUninstallItemMsixMissingName(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = origExec }()
	r := newTestRunner()

	item := msixItem
	item.DisplayName = "Missing Name App"
	item.Check.Appx.Name = ""

	_, err := r.uninstallItem(item, "")
	if err == nil {
		t.Fatalf("expected an error for missing Check.Appx.Name")
	}
	expected := "Check.Appx.Name is required for msix uninstall of Missing Name App"
	if have, want := err.Error(), expected; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
	if len(r.Report.UninstalledItems) != 0 {
		t.Errorf("failed msix uninstall must not be reported as uninstalled: %#v", r.Report.UninstalledItems)
	}
}

// TestUninstallStatusError verifies that Uninstall returns an error if the status check fails
func TestUninstallStatusError(t *testing.T) {
	// Override checkStatus with our fake version
	statusCheckStatus = fakeCheckStatus
	defer func() {
		statusCheckStatus = origCheckStatus
	}()

	// Run the msi uninstaller with this status bypass to trigger an error
	msiItem.DisplayName = statusNoActionError
	// Run Uninstall
	_, err := newTestRunner().Install(msiItem, "uninstall")
	// Check the result
	if err == nil {
		t.Fatalf("expected a status check error")
	}
	expectedOutput := "unable to check status: testing _gorilla_dev_noaction_error_"
	if have, want := err.Error(), expectedOutput; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
}

// TestUninstallStatusTrue verifies that Uninstall returns if status check is true
func TestUninstallStatusTrue(t *testing.T) {
	// Override checkStatus with our fake version
	statusCheckStatus = fakeCheckStatus
	defer func() {
		statusCheckStatus = origCheckStatus
	}()

	// Run the msi uninstaller with this status bypass to make status return true
	msiItem.DisplayName = statusNoActionNoError
	// Run Uninstall
	actualOutput, err := newTestRunner().Install(msiItem, "uninstall")
	if err != nil {
		t.Errorf("Install returned an error: %v", err)
	}
	// Check the result
	expectedOutput := "Item not needed"
	if have, want := actualOutput, expectedOutput; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
}

// TestUpdateStatusError verifies that Update returns an error if the status check fails
func TestUpdateStatusError(t *testing.T) {
	// Override checkStatus with our fake version
	statusCheckStatus = fakeCheckStatus
	defer func() {
		statusCheckStatus = origCheckStatus
	}()

	// Run the msi installer with this status bypass to trigger an error
	msiItem.DisplayName = statusActionError
	// Run Update
	_, err := newTestRunner().Install(msiItem, "update")
	// Check the result
	if err == nil {
		t.Fatalf("expected a status check error")
	}
	expectedOutput := "unable to check status: testing _gorilla_dev_action_error_"
	if have, want := err.Error(), expectedOutput; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
}

// TestUpdateStatusFalse verifies that Update returns if status check is false
func TestUpdateStatusFalse(t *testing.T) {
	// Override checkStatus with our fake version
	statusCheckStatus = fakeCheckStatus
	defer func() {
		statusCheckStatus = origCheckStatus
	}()

	// Run the msi installer with this status bypass to make status return dalse
	msiItem.DisplayName = statusNoActionNoError
	// Run Update
	actualOutput, err := newTestRunner().Install(msiItem, "update")
	if err != nil {
		t.Errorf("Install returned an error: %v", err)
	}
	// Check the result
	expectedOutput := "Item not needed"
	if have, want := actualOutput, expectedOutput; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
}

// TestInstallReport verifies that a successfully installed item is added to
// the report and not to FailedItems (K1)
func TestInstallReport(t *testing.T) {
	// Override execCommand with our fake version
	execCommand = fakeExecCommand
	defer func() {
		execCommand = origExec
	}()
	r := newTestRunner()

	// Run the installer
	msiItem.DisplayName = statusActionNoError
	_, err := r.installItem(msiItem, "https://example.com")
	if err != nil {
		t.Errorf("installItem returned an error: %v", err)
	}

	// Check the result
	expectedReport := []catalog.Item{msiItem}

	// Compare the result with our expectations
	structsMatch := reflect.DeepEqual(expectedReport, r.Report.InstalledItems)

	if !structsMatch {
		t.Errorf("\nExpected: %#v\nReceived: %#v", expectedReport, r.Report.InstalledItems)
	}
	if len(r.Report.FailedItems) != 0 {
		t.Errorf("successful install must not be in FailedItems: %#v", r.Report.FailedItems)
	}
}

// TestInstallItemFailureReport verifies that a failing installer command is
// returned as an error and the item lands in FailedItems, not InstalledItems (K1)
func TestInstallItemFailureReport(t *testing.T) {
	installTestOverrides(t)
	r := newTestRunner()

	// fakeRunCommand returns an error for this display name
	msiItem.DisplayName = statusActionError
	msiItem.Name = statusActionError

	_, err := r.installItem(msiItem, "https://example.com/")
	if err == nil {
		t.Fatalf("expected an error from a failing installer command")
	}
	if len(r.Report.InstalledItems) != 0 {
		t.Errorf("failed item must not be reported as installed: %#v", r.Report.InstalledItems)
	}
	if len(r.Report.FailedItems) != 1 {
		t.Fatalf("expected 1 failed item, got %#v", r.Report.FailedItems)
	}
	have := r.Report.FailedItems[0]
	if have.Name != statusActionError || have.Version != "1.2.3" || have.Action != "install" || have.Error == "" {
		t.Errorf("unexpected failed item entry: %#v", have)
	}
}

// TestProgressEvents verifies ordered real progress events reach the
// ProgressFn seam, including `failed` on error (K8-seam)
func TestProgressEvents(t *testing.T) {
	installTestOverrides(t)
	r := newTestRunner()

	var events, messages []string
	r.Emit = func(item catalog.Item, state string, percent int, message string) {
		events = append(events, fmt.Sprintf("%s:%d", state, percent))
		messages = append(messages, message)
	}

	// Success emits downloading -> installing -> done
	msiItem.DisplayName = statusActionNoError
	if _, err := r.installItem(msiItem, "https://example.com/"); err != nil {
		t.Fatalf("installItem returned an error: %v", err)
	}
	want := []string{"downloading:0", "installing:50", "done:100"}
	if !reflect.DeepEqual(want, events) {
		t.Errorf("\nExpected: %#v\nReceived: %#v", want, events)
	}

	// Failure emits downloading -> installing -> failed
	events = nil
	msiItem.DisplayName = statusActionError
	if _, err := r.installItem(msiItem, "https://example.com/"); err == nil {
		t.Fatalf("expected an error from a failing installer command")
	}
	want = []string{"downloading:0", "installing:50", "failed:50"}
	if !reflect.DeepEqual(want, events) {
		t.Errorf("\nExpected: %#v\nReceived: %#v", want, events)
	}

	// Uninstall emits removing
	events = nil
	msiItem.DisplayName = statusActionNoError
	if _, err := r.uninstallItem(msiItem, "https://example.com/"); err != nil {
		t.Fatalf("uninstallItem returned an error: %v", err)
	}
	want = []string{"downloading:0", "removing:50", "done:100"}
	if !reflect.DeepEqual(want, events) {
		t.Errorf("\nExpected: %#v\nReceived: %#v", want, events)
	}

	// Progress reaches every local user over the pipe, and a package URL can
	// carry signed-query tokens, so no message may name it.
	for _, message := range messages {
		if strings.Contains(message, "example.com") {
			t.Errorf("progress message %q leaks the package URL", message)
		}
	}
}

func fakeInstallItem(_ *Runner, item catalog.Item, itemURL string) (string, error) {
	installItemURL = itemURL
	return "", nil
}

// TestInstallURL validates that the url for an installer is properly generated
func TestInstallURL(t *testing.T) {
	// Override checkStatus and installItemFunc with our fake versions
	statusCheckStatus = fakeCheckStatus
	installItemFunc = fakeInstallItem
	defer func() {
		statusCheckStatus = origCheckStatus
		installItemFunc = origInstallItemFunc
	}()

	// Make sure the `installItemURL` variable is blank before we start
	installItemURL = ""

	// Run the msi installer with this status bypass checks
	msiItem.DisplayName = statusActionNoError

	// Run Install
	if _, err := newTestRunner().Install(msiItem, "install"); err != nil {
		t.Errorf("Install returned an error: %v", err)
	}

	// Check the result
	expectedURL := "https://example.com/packages/chef-client/chef-client-14.3.37-1-x64.msi"

	if have, want := installItemURL, expectedURL; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
}

func fakeUninstallItem(_ *Runner, item catalog.Item, itemURL string) (string, error) {
	uninstallItemURL = itemURL
	return "", nil
}

// TestUninstallURL validates that the url for an uninstaller is properly generated
func TestUninstallURL(t *testing.T) {
	// Override checkStatus and uninstallItemFunc with our fake versions
	statusCheckStatus = fakeCheckStatus
	uninstallItemFunc = fakeUninstallItem
	defer func() {
		statusCheckStatus = origCheckStatus
		uninstallItemFunc = (*Runner).uninstallItem
	}()

	// Make sure the `uninstallItemURL` variable is blank before we start
	uninstallItemURL = ""

	// Run the msi uninstaller with this status bypass checks
	msiItem.DisplayName = statusActionNoError

	// Run Install
	if _, err := newTestRunner().Install(msiItem, "uninstall"); err != nil {
		t.Errorf("Install returned an error: %v", err)
	}

	// Check the result
	expectedURL := "https://example.com/packages/chef-client/chef-client-14.3.37-1-x64uninst.msi"

	if have, want := uninstallItemURL, expectedURL; have != want {
		t.Errorf("\n-----\nhave\n%s\nwant\n%s\n-----", have, want)
	}
}

// captureConsole redirects gorillalog's console sink to a buffer (debug mode
// so DEBUG/INFO messages are visible) and restores it after the test.
func captureConsole(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	gorillalog.SetOutput(buf)
	cfg := config.Configuration{
		Debug:       true,
		Verbose:     true,
		AppDataPath: t.TempDir(),
	}
	// Register the Close cleanup AFTER t.TempDir(): cleanups run LIFO, so
	// Close releases the log file handle before TempDir's RemoveAll —
	// Windows cannot delete an open file.
	t.Cleanup(func() {
		gorillalog.SetOutput(os.Stdout)
		gorillalog.Close()
	})
	if err := gorillalog.NewLog(cfg); err != nil {
		t.Fatalf("NewLog failed: %v", err)
	}
	return buf
}

// TestRunCommandDebugOutput tests the output when running a command in debug
func TestRunCommandDebugOutput(t *testing.T) {
	console := captureConsole(t)

	// Override execCommand with our fake version
	execCommand = fakeExecCommand
	defer func() { execCommand = origExec }()

	// Set up what we expect
	testCmd := "Command Test!"
	testArgs := []string{"arg1", "arg2"}

	// Run the function
	runCommand(testCmd, testArgs)

	out := console.String()
	for _, want := range []string{
		`msg="Running command" command="Command Test!" args="[arg1 arg2]"`,
		`msg="Command output" output="[Command Test! arg1 arg2]"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("console output missing %q:\n%s", want, out)
		}
	}
}

// installTestOverrides swaps execCommand/checkStatus/runCommand for fakes and
// restores them after the test.
func installTestOverrides(t *testing.T) {
	t.Helper()
	execCommand = fakeExecCommand
	statusCheckStatus = fakeCheckStatus
	runCommand = fakeRunCommand
	download.SetConfig(downloadCfg)
	t.Cleanup(func() {
		execCommand = origExec
		statusCheckStatus = origCheckStatus
		runCommand = origRunCommand
	})
}

func TestInstallItemSuccess(t *testing.T) {
	console := captureConsole(t)
	installTestOverrides(t)
	r := newTestRunner()

	// Msi
	msiItem.DisplayName = statusActionNoError

	// Run Install
	_, _ = r.installItem(msiItem, "https://example.com/")

	out := console.String()
	for _, want := range []string{
		`msg=Installing item=_gorilla_dev_action_noerror_ version=1.2.3 installerType=msi`,
		`msg="Installation SUCCESSFUL" item=_gorilla_dev_action_noerror_ version=1.2.3 result=success`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("console output missing %q:\n%s", want, out)
		}
	}
}

func TestInstallItemFailure(t *testing.T) {
	console := captureConsole(t)
	installTestOverrides(t)
	r := newTestRunner()

	// Msi
	msiItem.DisplayName = statusActionError

	// Run Install
	_, _ = r.installItem(msiItem, "https://example.com/")

	out := console.String()
	for _, want := range []string{
		`msg=Installing item=_gorilla_dev_action_error_ version=1.2.3 installerType=msi`,
		`msg="Installation FAILED" item=_gorilla_dev_action_error_ version=1.2.3 result=error err="Deliberate test error has occurred!!"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("console output missing %q:\n%s", want, out)
		}
	}
}

func TestUninstallItemSuccess(t *testing.T) {
	console := captureConsole(t)
	installTestOverrides(t)
	r := newTestRunner()

	// Msi
	msiItem.DisplayName = statusActionNoError

	// Run Uninstall
	_, _ = r.uninstallItem(msiItem, "https://example.com/")

	out := console.String()
	for _, want := range []string{
		`msg=Uninstalling item=_gorilla_dev_action_noerror_ version=1.2.3 installerType=msi`,
		`msg="Uninstallation SUCCESSFUL" item=_gorilla_dev_action_noerror_ version=1.2.3 result=success`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("console output missing %q:\n%s", want, out)
		}
	}
}

func TestUninstallItemFailure(t *testing.T) {
	console := captureConsole(t)
	installTestOverrides(t)
	r := newTestRunner()

	// Msi
	msiItem.DisplayName = statusActionError

	// Run Uninstall
	_, _ = r.uninstallItem(msiItem, "https://example.com/")

	out := console.String()
	for _, want := range []string{
		`msg=Uninstalling item=_gorilla_dev_action_error_ version=1.2.3 installerType=msi`,
		`msg="Uninstallation FAILED" item=_gorilla_dev_action_error_ version=1.2.3 result=error err="Deliberate test error has occurred!!"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("console output missing %q:\n%s", want, out)
		}
	}
}

// TestPrePostScriptsDistinctTempFiles verifies that pre- and post-install
// scripts run from distinct temporary files that are removed afterwards (K6)
func TestPrePostScriptsDistinctTempFiles(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	installItemFunc = fakeInstallItem
	var scriptFiles []string
	execCommand = func(command string, args ...string) *exec.Cmd {
		// The script path is the final argument (after -File)
		scriptFiles = append(scriptFiles, args[len(args)-1])
		return fakeExecCommand(command, args...)
	}
	defer func() {
		statusCheckStatus = origCheckStatus
		installItemFunc = origInstallItemFunc
		execCommand = origExec
	}()

	item := msiItem
	item.DisplayName = statusActionNoError
	item.PreScript = "Write-Output pre"
	item.PostScript = "Write-Output post"

	if _, err := newTestRunner().Install(item, "install"); err != nil {
		t.Fatalf("Install returned an error: %v", err)
	}

	if len(scriptFiles) != 2 {
		t.Fatalf("expected 2 script executions, got %d: %#v", len(scriptFiles), scriptFiles)
	}
	pre, post := scriptFiles[0], scriptFiles[1]
	if pre == post {
		t.Errorf("pre and post scripts shared the same temp file: %s", pre)
	}
	if !strings.HasPrefix(filepath.Base(pre), "gorilla-preinstall-") {
		t.Errorf("unexpected preinstall temp file name: %s", pre)
	}
	if !strings.HasPrefix(filepath.Base(post), "gorilla-postinstall-") {
		t.Errorf("unexpected postinstall temp file name: %s", post)
	}
	// Both temp files must be removed after the run
	for _, f := range scriptFiles {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("temp script was not removed: %s", f)
		}
	}
}

// TestPreScriptFailure verifies that a failing pre-install script returns an
// error (no panic) and skips the installer
func TestPreScriptFailure(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	installItemFunc = func(_ *Runner, item catalog.Item, itemURL string) (string, error) {
		t.Error("installer ran despite pre-install script failure")
		return "", nil
	}
	execCommand = fakeExecCommandFail
	defer func() {
		statusCheckStatus = origCheckStatus
		installItemFunc = origInstallItemFunc
		execCommand = origExec
	}()
	r := newTestRunner()

	item := msiItem
	item.DisplayName = statusActionNoError
	item.PreScript = "exit 1"

	_, err := r.Install(item, "install")
	if err == nil {
		t.Fatalf("expected a pre-install script error")
	}
	if !strings.Contains(err.Error(), "pre-install script error") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(r.Report.FailedItems) != 1 {
		t.Errorf("expected 1 failed item, got %#v", r.Report.FailedItems)
	}
}

// TestPostScriptFailure verifies that a failing post-install script returns
// an error (no panic)
func TestPostScriptFailure(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	installItemFunc = fakeInstallItem
	execCommand = fakeExecCommandFail
	defer func() {
		statusCheckStatus = origCheckStatus
		installItemFunc = origInstallItemFunc
		execCommand = origExec
	}()
	r := newTestRunner()

	item := msiItem
	item.DisplayName = statusActionNoError
	item.PostScript = "exit 1"

	_, err := r.Install(item, "install")
	if err == nil {
		t.Fatalf("expected a post-install script error")
	}
	if !strings.Contains(err.Error(), "post-install script error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestPreUninstallScriptFailure verifies that a failing pre-uninstall script
// aborts the uninstall (no uninstall attempted) and records the failure.
func TestPreUninstallScriptFailure(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	uninstallItemFunc = func(_ *Runner, item catalog.Item, itemURL string) (string, error) {
		t.Error("uninstaller ran despite pre-uninstall script failure")
		return "", nil
	}
	execCommand = fakeExecCommandFail
	defer func() {
		statusCheckStatus = origCheckStatus
		uninstallItemFunc = (*Runner).uninstallItem
		execCommand = origExec
	}()
	r := newTestRunner()

	item := msiItem
	item.DisplayName = statusActionNoError
	item.PreUninstallScript = "exit 1"

	_, err := r.Install(item, "uninstall")
	if err == nil {
		t.Fatalf("expected a pre-uninstall script error")
	}
	if !strings.Contains(err.Error(), "pre-uninstall script error") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(r.Report.FailedItems) != 1 {
		t.Errorf("expected 1 failed item, got %#v", r.Report.FailedItems)
	}
}

// TestPostUninstallScriptFailure verifies that a failing post-uninstall script
// double-records: the item genuinely uninstalled (UninstalledItems) plus the
// script failure (FailedItems).
func TestPostUninstallScriptFailure(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	uninstallItemFunc = func(r *Runner, item catalog.Item, itemURL string) (string, error) {
		r.Report.UninstalledItems = append(r.Report.UninstalledItems, item)
		return "", nil
	}
	execCommand = fakeExecCommandFail
	defer func() {
		statusCheckStatus = origCheckStatus
		uninstallItemFunc = (*Runner).uninstallItem
		execCommand = origExec
	}()
	r := newTestRunner()

	item := msiItem
	item.DisplayName = statusActionNoError
	item.PostUninstallScript = "exit 1"

	_, err := r.Install(item, "uninstall")
	if err == nil {
		t.Fatalf("expected a post-uninstall script error")
	}
	if !strings.Contains(err.Error(), "post-uninstall script error") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(r.Report.UninstalledItems) != 1 {
		t.Errorf("expected item to be recorded uninstalled, got %#v", r.Report.UninstalledItems)
	}
	if len(r.Report.FailedItems) != 1 {
		t.Errorf("expected 1 failed item, got %#v", r.Report.FailedItems)
	}
}

// TestPrePostUninstallScriptsSuccess verifies the happy path: both scripts run
// (distinct temp files) and the uninstall records no failure.
func TestPrePostUninstallScriptsSuccess(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	uninstallItemFunc = fakeUninstallItem
	var scriptFiles []string
	execCommand = func(command string, args ...string) *exec.Cmd {
		scriptFiles = append(scriptFiles, args[len(args)-1])
		return fakeExecCommand(command, args...)
	}
	defer func() {
		statusCheckStatus = origCheckStatus
		uninstallItemFunc = (*Runner).uninstallItem
		execCommand = origExec
	}()
	r := newTestRunner()

	item := msiItem
	item.DisplayName = statusActionNoError
	item.PreUninstallScript = "Write-Output pre"
	item.PostUninstallScript = "Write-Output post"

	if _, err := r.Install(item, "uninstall"); err != nil {
		t.Fatalf("Install returned an error: %v", err)
	}
	if len(scriptFiles) != 2 {
		t.Fatalf("expected 2 script executions, got %d: %#v", len(scriptFiles), scriptFiles)
	}
	if !strings.HasPrefix(filepath.Base(scriptFiles[0]), "gorilla-preuninstall-") {
		t.Errorf("unexpected preuninstall temp file name: %s", scriptFiles[0])
	}
	if !strings.HasPrefix(filepath.Base(scriptFiles[1]), "gorilla-postuninstall-") {
		t.Errorf("unexpected postuninstall temp file name: %s", scriptFiles[1])
	}
	if len(r.Report.FailedItems) != 0 {
		t.Errorf("expected no failed items, got %#v", r.Report.FailedItems)
	}
}

// origBlockingApps restores the blocking-app snapshot seam after each gate test.
var origBlockingApps = runningBlockingApps

// TestBlockingGateDefers verifies a running blocking app defers the install:
// ErrBlockingApps returned, a DeferredItem recorded (by catalog key Name), and
// nothing in FailedItems or InstalledItems (spec R6/R12).
func TestBlockingGateDefers(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	installItemFunc = func(_ *Runner, item catalog.Item, itemURL string) (string, error) {
		t.Error("installer ran despite a running blocking app")
		return "", nil
	}
	runningBlockingApps = func(apps []string) ([]string, error) { return []string{"notepad.exe"}, nil }
	defer func() {
		statusCheckStatus = origCheckStatus
		installItemFunc = origInstallItemFunc
		runningBlockingApps = origBlockingApps
	}()

	item := msiItem
	item.Name = "DemoBlocked"
	item.DisplayName = statusActionNoError
	item.BlockingApps = []string{"notepad"}

	r := newTestRunner()
	_, err := r.Install(item, "install")
	if !errors.Is(err, ErrBlockingApps) {
		t.Fatalf("expected ErrBlockingApps, got %v", err)
	}
	if len(r.Report.DeferredItems) != 1 {
		t.Fatalf("expected 1 deferred item, got %#v", r.Report.DeferredItems)
	}
	if d := r.Report.DeferredItems[0]; d.Name != "DemoBlocked" || d.Action != "install" || !strings.Contains(d.Reason, "notepad") {
		t.Errorf("unexpected deferred item: %#v", d)
	}
	if len(r.Report.FailedItems) != 0 {
		t.Errorf("deferred item must not be in FailedItems: %#v", r.Report.FailedItems)
	}
	if len(r.Report.InstalledItems) != 0 {
		t.Errorf("deferred item must not be in InstalledItems: %#v", r.Report.InstalledItems)
	}
}

// TestBlockingGateOnlyWhenActionNeeded verifies an already-satisfied item is
// never deferred: the gate lives behind the actionNeeded check, so the blocking
// snapshot is never even taken (spec R6).
func TestBlockingGateOnlyWhenActionNeeded(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	runningBlockingApps = func(apps []string) ([]string, error) {
		t.Error("blocking snapshot taken for an already-satisfied item")
		return []string{"notepad.exe"}, nil
	}
	defer func() {
		statusCheckStatus = origCheckStatus
		runningBlockingApps = origBlockingApps
	}()

	item := msiItem
	item.DisplayName = statusNoActionNoError // no action needed
	item.BlockingApps = []string{"notepad"}

	r := newTestRunner()
	if _, err := r.Install(item, "install"); err != nil {
		t.Fatalf("Install returned an error: %v", err)
	}
	if len(r.Report.DeferredItems) != 0 {
		t.Errorf("already-satisfied item must not be deferred: %#v", r.Report.DeferredItems)
	}
}

// TestBlockingGateUninstall verifies the uninstall arm is gated too.
func TestBlockingGateUninstall(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	uninstallItemFunc = func(_ *Runner, item catalog.Item, itemURL string) (string, error) {
		t.Error("uninstaller ran despite a running blocking app")
		return "", nil
	}
	runningBlockingApps = func(apps []string) ([]string, error) { return []string{"notepad.exe"}, nil }
	defer func() {
		statusCheckStatus = origCheckStatus
		uninstallItemFunc = (*Runner).uninstallItem
		runningBlockingApps = origBlockingApps
	}()

	item := msiItem
	item.Name = "DemoBlocked"
	item.DisplayName = statusActionNoError
	item.BlockingApps = []string{"notepad"}

	r := newTestRunner()
	_, err := r.Install(item, "uninstall")
	if !errors.Is(err, ErrBlockingApps) {
		t.Fatalf("expected ErrBlockingApps, got %v", err)
	}
	if len(r.Report.DeferredItems) != 1 || r.Report.DeferredItems[0].Action != "uninstall" {
		t.Errorf("expected 1 uninstall deferral, got %#v", r.Report.DeferredItems)
	}
}

// TestBlockingGateSnapshotErrorProceeds verifies a snapshot error does not block
// the run: the action proceeds (Munki treats a failed listing as none running).
func TestBlockingGateSnapshotErrorProceeds(t *testing.T) {
	statusCheckStatus = fakeCheckStatus
	installItemFunc = fakeInstallItem
	runningBlockingApps = func(apps []string) ([]string, error) { return nil, fmt.Errorf("snapshot boom") }
	defer func() {
		statusCheckStatus = origCheckStatus
		installItemFunc = origInstallItemFunc
		runningBlockingApps = origBlockingApps
	}()

	item := msiItem
	item.DisplayName = statusActionNoError
	item.BlockingApps = []string{"notepad"}

	r := newTestRunner()
	if _, err := r.Install(item, "install"); err != nil {
		t.Fatalf("snapshot error should proceed, got %v", err)
	}
	if len(r.Report.DeferredItems) != 0 {
		t.Errorf("snapshot error must not defer: %#v", r.Report.DeferredItems)
	}
}
