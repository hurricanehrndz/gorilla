package status

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/download"
	version "github.com/hashicorp/go-version"
)

// RegistryApplication contains attributes for an installed application
type RegistryApplication struct {
	Key       string
	Location  string
	Name      string
	Source    string
	Uninstall string
	Version   string
}

// WindowsMetadata contains extended metadata retrieved in the `properties.go`
type WindowsMetadata struct {
	productName   string
	companyName   string
	versionString string
	versionMajor  int
	versionMinor  int
	versionPatch  int
	versionBuild  int
}

// Abstracted functions so we can override these in unit tests
var execCommand = exec.Command

// Checker holds the run-scoped status state (K7: no cross-run package globals)
type Checker struct {
	// registryItems caches the applications found in the registry,
	// lazily populated on first registry check
	registryItems map[string]RegistryApplication
}

// checkRegistry iterates through the local registry and compiles all installed software
func (c *Checker) checkRegistry(catalogItem catalog.Item, installType string) (actionNeeded bool, checkErr error) {
	// Iterate through the reg keys to compare with the catalog
	checkReg := catalogItem.Check.Registry
	catalogVersion, err := version.NewVersion(checkReg.Version)
	if err != nil {
		slog.Warn("Unable to parse new version", "version", checkReg.Version, "err", err)
	}

	slog.Debug("Check registry version", "version", checkReg.Version)
	// If needed, populate applications status from the registry
	if len(c.registryItems) == 0 {
		c.registryItems, checkErr = getUninstallKeys()
	}

	var installed bool
	var versionMatch bool
	for _, regItem := range c.registryItems {
		// Check if the catalog name matches the registry name (K2: exact match, not substring)
		if !strings.EqualFold(strings.TrimSpace(regItem.Name), strings.TrimSpace(checkReg.Name)) {
			continue
		}
		installed = true
		slog.Debug("Current installed version", "version", regItem.Version)

		// If the catalog version is unparseable, fall back to exact string equality
		if catalogVersion == nil {
			if regItem.Version == checkReg.Version {
				versionMatch = true
			}
			continue
		}

		// Check if the catalog version matches the registry
		// K3: evaluate every matching entry (no break) and treat an unparseable
		// installed version as not a match instead of dereferencing nil
		currentVersion, err := version.NewVersion(regItem.Version)
		if err != nil {
			slog.Warn("Unable to parse current version", "version", regItem.Version, "err", err)
			continue
		}
		if !currentVersion.LessThan(catalogVersion) {
			versionMatch = true
		}
	}

	if installType == "update" && !installed {
		actionNeeded = false
	} else if installType == "uninstall" {
		actionNeeded = installed
	} else if installed && versionMatch {
		actionNeeded = false
	} else {
		actionNeeded = true
	}

	return actionNeeded, checkErr
}

func checkScript(catalogItem catalog.Item, cachePath string, installType string) (actionNeeded bool, checkErr error) {
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		return false, err
	}

	// Write InstallCheckScript to disk as a Powershell file
	tmpFile, err := os.CreateTemp(cachePath, "gorilla-check-*.ps1")
	if err != nil {
		return false, err
	}
	tmpScript := tmpFile.Name()
	defer func() {
		if removeErr := os.Remove(tmpScript); removeErr != nil && !os.IsNotExist(removeErr) {
			slog.Warn("Unable to remove temporary check script", "path", tmpScript, "err", removeErr)
		}
	}()
	_, writeErr := tmpFile.WriteString(catalogItem.Check.Script)
	closeErr := tmpFile.Close()
	if writeErr != nil {
		return false, writeErr
	}
	if closeErr != nil {
		return false, closeErr
	}

	// Build the command to execute the script
	psCmd := filepath.Join(os.Getenv("WINDIR"), "system32/", "WindowsPowershell", "v1.0", "powershell.exe")
	psArgs := []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", tmpScript}

	// Execute the script
	cmd := execCommand(psCmd, psArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	cmdSuccess := cmd.ProcessState.Success()
	outStr, errStr := stdout.String(), stderr.String()

	// Log results
	slog.Debug("Command error", "err", err)
	slog.Debug("Command stdout", "stdout", outStr)
	slog.Debug("Command stderr", "stderr", errStr)

	actionNeeded = false
	// Application not installed if exit 0
	if installType == "uninstall" {
		actionNeeded = !cmdSuccess
	} else if installType == "install" || installType == "update" {
		actionNeeded = cmdSuccess
	}

	return actionNeeded, checkErr
}

func checkPath(catalogItem catalog.Item, installType string) (actionNeeded bool, checkErr error) {
	var actionStore []bool

	// Iterate through all file provided paths
	for _, checkFile := range catalogItem.Check.File {
		path := filepath.Clean(checkFile.Path)
		slog.Debug("Check file path", "path", path)
		_, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {

				// when doing an install, and the file path does not exist
				// perform an install
				if installType == "install" {
					actionStore = append(actionStore, true)
					break
				}

				// When doing an update or uninstall, and the file path does
				// not exist, do nothing
				if installType == "update" || installType == "uninstall" {
					slog.Debug("No action needed", "installType", installType)
					break
				}
			}
			slog.Warn("Unable to check path", "path", path, "err", err)
			break

		} else if err == nil {
			// When doing an uninstall, and the path exists
			// perform uninstall
			if installType == "uninstall" {
				actionStore = append(actionStore, true)
			}
		}

		// If a hash is not blank, verify it matches the file
		// if the hash does not match, we need to install
		if checkFile.Hash != "" {
			slog.Debug("Check file hash", "hash", checkFile.Hash)
			hashMatch := download.Verify(path, checkFile.Hash)
			if !hashMatch {
				actionStore = append(actionStore, true)
				break
			}
		}

		if checkFile.Version != "" {
			slog.Debug("Check file version", "version", checkFile.Version)

			// Get the file metadata, and check that it has a value
			metadata := GetFileMetadata(path)
			if metadata.versionString == "" {
				break
			}
			slog.Debug("Current installed version", "version", metadata.versionString)

			// Convert both strings to a `Version` object
			versionHave, err := version.NewVersion(metadata.versionString)
			if err != nil {
				slog.Warn("Unable to compare version", "version", metadata.versionString)
				actionStore = append(actionStore, true)
				break
			}
			versionWant, err := version.NewVersion(checkFile.Version)
			if err != nil {
				slog.Warn("Unable to compare version", "version", checkFile.Version)
				actionStore = append(actionStore, true)
				break
			}

			// Compare the versions
			outdated := versionHave.LessThan(versionWant)
			if outdated {
				actionStore = append(actionStore, true)
				break
			}
		}
	}

	for _, item := range actionStore {
		if item {
			actionNeeded = true
			return
		}
	}
	actionNeeded = false
	return actionNeeded, checkErr
}

// checkAppx checks whether an AppX/MSIX package is provisioned and at the required version.
// It calls `Get-AppxProvisionedPackage -Online` via PowerShell and parses the Version field
// from the output to compare against the catalog version.
func checkAppx(catalogItem catalog.Item, installType string) (actionNeeded bool, checkErr error) {
	checkAppxItem := catalogItem.Check.Appx
	psCmd := filepath.Join(os.Getenv("WINDIR"), "system32/", "WindowsPowershell", "v1.0", "powershell.exe")
	psArgs := []string{
		"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command",
		fmt.Sprintf(
			"$p = Get-AppxProvisionedPackage -Online | Where-Object { $_.DisplayName -eq '%s' }; if ($p) { $p.Version } else { '' }",
			checkAppxItem.Name,
		),
	}

	cmd := execCommand(psCmd, psArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		slog.Warn("checkAppx command error", "err", err)
	}

	installedVersionStr := strings.TrimSpace(stdout.String())
	slog.Debug("AppX installed version", "version", installedVersionStr)
	slog.Debug("AppX stderr", "stderr", stderr.String())

	installed := installedVersionStr != ""

	var versionMatch bool
	if installed && checkAppxItem.Version != "" {
		catalogVersion, err := version.NewVersion(checkAppxItem.Version)
		if err != nil {
			slog.Warn("Unable to parse catalog appx version", "version", checkAppxItem.Version, "err", err)
			return true, err
		}
		currentVersion, err := version.NewVersion(installedVersionStr)
		if err != nil {
			slog.Warn("Unable to parse installed appx version", "version", installedVersionStr, "err", err)
			return true, err
		}
		versionMatch = !currentVersion.LessThan(catalogVersion)
	}

	if installType == "update" && !installed {
		actionNeeded = false
	} else if installType == "uninstall" {
		actionNeeded = installed
	} else if installed && versionMatch {
		actionNeeded = false
	} else {
		actionNeeded = true
	}

	return actionNeeded, checkErr
}

// CheckStatus determines the method for checking status
func (c *Checker) CheckStatus(catalogItem catalog.Item, installType, cachePath string) (actionNeeded bool, checkErr error) {
	if catalogItem.Check.Script != "" {
		slog.Info("Checking status via script", "item", catalogItem.DisplayName)
		return checkScript(catalogItem, cachePath, installType)

	} else if len(catalogItem.Check.File) > 0 {
		slog.Info("Checking status via file", "item", catalogItem.DisplayName)
		return checkPath(catalogItem, installType)

	} else if catalogItem.Check.Registry.Version != "" {
		slog.Info("Checking status via registry", "item", catalogItem.DisplayName)
		return c.checkRegistry(catalogItem, installType)

	} else if catalogItem.Check.Appx.Name != "" {
		slog.Info("Checking status via appx", "item", catalogItem.DisplayName)
		return checkAppx(catalogItem, installType)
	}

	slog.Warn("Not enough data to check the current status", "item", catalogItem.DisplayName)
	return
}
