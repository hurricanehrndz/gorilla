package installer

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/download"
	"github.com/1dustindavis/gorilla/pkg/report"
	"github.com/1dustindavis/gorilla/pkg/status"
)

// ErrBlockingApps is returned by Install when a listed blocking application is
// running: the action was skipped and recorded in the report's DeferredItems
// (spec R6). It is errors.Is-able so the dependency walk can cascade the
// deferral instead of treating it as a failure.
var ErrBlockingApps = errors.New("blocking applications running")

// runningBlockingApps is a seam so tests can stub the process snapshot.
var runningBlockingApps = status.RunningBlockingApps

// ProgressFn receives coarse per-item progress events. States emitted per
// item: `downloading`, `installing`/`removing`, `done`/`failed`. The service
// attaches an operation-scoped callback for self-service mutations.
type ProgressFn func(item catalog.Item, state string, percent int, message string)

// Runner is the run-scoped install context (K7: no cross-run package globals)
type Runner struct {
	Report      *report.Report
	Checker     *status.Checker
	Emit        ProgressFn // nil = no progress events
	Cancels     *Cancels   // nil = nothing can be withdrawn
	URLPackages string
	CachePath   string
	CheckOnly   bool
}

// emit sends a progress event if a sink is attached
func (r *Runner) emit(item catalog.Item, state string, percent int, message string) {
	if r.Emit != nil {
		r.Emit(item, state, percent, message)
	}
}

var (
	// Base command for each installer type
	commandNupkg = filepath.Join(os.Getenv("ProgramData"), "chocolatey/bin/choco.exe")
	commandMsi   = filepath.Join(os.Getenv("WINDIR"), "system32/", "msiexec.exe")
	commandPs1   = filepath.Join(os.Getenv("WINDIR"), "system32/", "WindowsPowershell", "v1.0", "powershell.exe")

	// These abstractions allows us to override when testing
	execCommand       = exec.Command
	statusCheckStatus = (*status.Checker).CheckStatus
	runCommand        = runCMD
)

// runCommand executes a command and it's argurments in the CMD environment
func runCMD(command string, arguments []string) (string, error) {
	cmd := execCommand(command, arguments...)
	var cmdOutput []string
	cmdReader, err := cmd.StdoutPipe()
	if err != nil {
		slog.Warn("Error creating pipe to stdout", "command", command, "args", arguments, "err", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)

	scanner := bufio.NewScanner(cmdReader)
	slog.Debug("Running command", "command", command, "args", arguments)
	go func() {
		for scanner.Scan() {
			cmdOutput = append(cmdOutput, scanner.Text())
		}
		wg.Done()
	}()

	err = cmd.Start()
	if err != nil {
		slog.Warn("Error running command", "command", command, "args", arguments, "err", err)
	}

	wg.Wait()
	err = cmd.Wait()
	output := strings.Join(cmdOutput, "\n")
	slog.Debug("Command output", "output", output)
	if err != nil {
		slog.Warn("Command error", "command", command, "args", arguments, "err", err)
	}

	return output, err
}

// Get a Nupkg's id using `choco list`
func getNupkgIDs(nupkgDir, versionArg string) ([]string, error) {
	// Compile the arguments needed to get the id
	command := commandNupkg
	arguments := []string{"list", versionArg, "--id-only", "-r", "-s", nupkgDir}

	// Run the command and parse each non-empty line as a candidate id
	cmdOut, cmdErr := runCommand(command, arguments)
	outputLines := strings.Split(cmdOut, "\n")
	ids := make([]string, 0, len(outputLines))
	for _, line := range outputLines {
		candidate := strings.TrimSpace(line)
		if candidate == "" {
			continue
		}
		ids = append(ids, candidate)
	}

	return ids, cmdErr
}

func resolveNupkgID(itemName, nupkgDir, versionArg, packageID string) (string, error) {
	explicitID := strings.TrimSpace(packageID)
	if explicitID != "" {
		return explicitID, nil
	}

	if versionArg == "" {
		return "", nil
	}

	ids, cmdErr := getNupkgIDs(nupkgDir, versionArg)
	if cmdErr != nil {
		return "", cmdErr
	}

	if len(ids) == 0 {
		return "", fmt.Errorf("no package id was found for %s; set installer/uninstaller.package_id", itemName)
	}

	if len(ids) > 1 {
		return "", fmt.Errorf("multiple package ids were found for %s: %s; set installer/uninstaller.package_id", itemName, strings.Join(ids, ", "))
	}

	return ids[0], nil
}

// typeInstaller translates a catalog item into the command and arguments for
// one installer type.
type typeInstaller interface {
	installCommand(item catalog.Item, absFile string) (cmd string, args []string, err error)
	uninstallCommand(item catalog.Item, absFile string) (cmd string, args []string, err error)
	uninstallNeedsFile() bool
}

// typeInstallers maps each supported installer type to its implementation.
var typeInstallers = map[string]typeInstaller{
	"msi":   msiInstaller{},
	"exe":   exeInstaller{},
	"ps1":   ps1Installer{},
	"msix":  msixInstaller{},
	"nupkg": nupkgInstaller{},
}

type msiInstaller struct{}

func (msiInstaller) installCommand(item catalog.Item, absFile string) (string, []string, error) {
	args := []string{"/i", absFile, "/qn", "/norestart"}
	args = append(args, item.Installer.Arguments...)
	return commandMsi, args, nil
}

func (msiInstaller) uninstallCommand(item catalog.Item, absFile string) (string, []string, error) {
	return commandMsi, []string{"/x", absFile, "/qn", "/norestart"}, nil
}

func (msiInstaller) uninstallNeedsFile() bool { return true }

type exeInstaller struct{}

func (exeInstaller) installCommand(item catalog.Item, absFile string) (string, []string, error) {
	return absFile, item.Installer.Arguments, nil
}

func (exeInstaller) uninstallCommand(item catalog.Item, absFile string) (string, []string, error) {
	return absFile, item.Uninstaller.Arguments, nil
}

func (exeInstaller) uninstallNeedsFile() bool { return true }

type ps1Installer struct{}

func (ps1Installer) installCommand(item catalog.Item, absFile string) (string, []string, error) {
	return commandPs1, []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", absFile}, nil
}

func (ps1Installer) uninstallCommand(item catalog.Item, absFile string) (string, []string, error) {
	return commandPs1, []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", absFile}, nil
}

func (ps1Installer) uninstallNeedsFile() bool { return true }

type msixInstaller struct{}

func (msixInstaller) installCommand(item catalog.Item, absFile string) (string, []string, error) {
	psCommand := fmt.Sprintf("Add-AppxProvisionedPackage -Online -PackagePath '%s' -SkipLicense", absFile)
	if len(item.Installer.Arguments) > 0 {
		psCommand += " " + strings.Join(item.Installer.Arguments, " ")
	}
	return commandPs1, []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psCommand}, nil
}

func (msixInstaller) uninstallCommand(item catalog.Item, absFile string) (string, []string, error) {
	if item.Check.Appx.Name == "" {
		return "", nil, fmt.Errorf("Check.Appx.Name is required for msix uninstall of %s", item.DisplayName)
	}
	removeCmd := fmt.Sprintf(
		"$pkg = Get-AppxProvisionedPackage -Online | Where-Object { $_.DisplayName -eq '%s' }; if ($pkg) { Remove-AppxProvisionedPackage -Online -PackageName $pkg.PackageName }; Get-AppxPackage -Name '%s' -AllUsers | Remove-AppxPackage -AllUsers -ErrorAction SilentlyContinue",
		item.Check.Appx.Name, item.Check.Appx.Name,
	)
	return commandPs1, []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", removeCmd}, nil
}

func (msixInstaller) uninstallNeedsFile() bool { return false }

type nupkgInstaller struct{}

// nupkgCommand builds the choco command shared by install and uninstall.
func nupkgCommand(verb string, item catalog.Item, absFile, packageID string) (string, []string, error) {
	// choco wants the "id" and parent dir, so we need to determine both
	slog.Info("Determining nupkg id", "item", item.DisplayName)
	nupkgDir := filepath.Dir(absFile)

	// Since choco recommends the source is a directory,
	// we need to pass a version to filter unexpected nupkgs (if we have a version)
	var versionArg string
	if item.Version != "" {
		versionArg = fmt.Sprintf("--version=%s", item.Version)
	}

	nupkgID, err := resolveNupkgID(item.DisplayName, nupkgDir, versionArg, packageID)
	if err != nil {
		return "", nil, fmt.Errorf("unable to determine nupkg id for %s: %w", item.DisplayName, err)
	}

	// Now pass the id along with the parent directory
	var args []string
	if nupkgID != "" && versionArg != "" {
		// Only use this form if we have an ID and version number
		args = []string{verb, nupkgID, "-s", nupkgDir, versionArg, "-f", "-y", "-r"}
	} else {
		// If we dont have an id and version, fallback to the method choco doesn't recommend (but works)
		args = []string{verb, absFile, "-f", "-y", "-r"}
	}
	return commandNupkg, args, nil
}

func (nupkgInstaller) installCommand(item catalog.Item, absFile string) (string, []string, error) {
	return nupkgCommand("install", item, absFile, item.Installer.PackageID)
}

func (nupkgInstaller) uninstallCommand(item catalog.Item, absFile string) (string, []string, error) {
	return nupkgCommand("uninstall", item, absFile, item.Uninstaller.PackageID)
}

func (nupkgInstaller) uninstallNeedsFile() bool { return true }

// canceled logs that the user withdrew the item and returns ErrCanceled. The
// service already ended the operation, so no progress event is sent.
func (r *Runner) canceled(item catalog.Item) error {
	slog.Info("Skipping item: canceled by user", "item", item.DisplayName)
	return ErrCanceled
}

// recordFailure appends the item to the run report's FailedItems and returns the error.
func (r *Runner) recordFailure(item catalog.Item, action string, err error) error {
	r.Report.FailedItems = append(r.Report.FailedItems, report.FailedItem{
		Name:    item.Name,
		Version: item.Version,
		Action:  action,
		Error:   err.Error(),
	})
	return err
}

// blockingGate defers the item when any of its explicit blocking_apps are
// running (spec R6): it records a DeferredItem and returns ErrBlockingApps so
// the caller skips — never kills — the action. An empty list means no check. A
// snapshot failure proceeds (does not block the run), matching Munki, which
// treats a failed process listing as "none running".
func (r *Runner) blockingGate(item catalog.Item, action string) error {
	if len(item.BlockingApps) == 0 {
		return nil
	}
	running, err := runningBlockingApps(item.BlockingApps)
	if err != nil {
		slog.Warn("unable to check blocking apps, proceeding", "item", item.DisplayName, "err", err)
		return nil
	}
	if len(running) == 0 {
		return nil
	}
	r.Report.DeferredItems = append(r.Report.DeferredItems, report.DeferredItem{
		Name:    item.Name,
		Version: item.Version,
		Action:  action,
		Reason:  "blocking application(s) running: " + strings.Join(running, ", "),
	})
	slog.Info("Deferring item: blocking application(s) running", "item", item.DisplayName, "apps", strings.Join(running, ", "))
	return ErrBlockingApps
}

// actionItem is the shared execution path for installs and uninstalls:
// download if needed, build the type-specific command, run it, and record the
// honest result in the report.
func (r *Runner) actionItem(item catalog.Item, itemURL, action string) (string, error) {
	var installerItem catalog.InstallerItem
	var state, verb string
	if action == "uninstall" {
		installerItem = item.Uninstaller
		// msix items may omit the uninstaller block; infer from the installer
		if installerItem.Type == "" && item.Installer.Type == "msix" {
			installerItem.Type = "msix"
		}
		state, verb = "removing", "Uninstall"
	} else {
		installerItem = item.Installer
		state, verb = "installing", "Install"
	}

	impl, ok := typeInstallers[installerItem.Type]
	if !ok {
		err := fmt.Errorf("unsupported %ser type %q for %s", action, installerItem.Type, item.DisplayName)
		r.emit(item, "failed", 0, err.Error())
		return "", r.recordFailure(item, action, err)
	}

	// Download the item if this action needs the file on disk
	var absFile string
	percent := 0
	if action == "install" || impl.uninstallNeedsFile() {
		relPath, fileName := path.Split(installerItem.Location)
		absFile = filepath.Join(r.CachePath, relPath, fileName)
		ctx, done, ok := r.Cancels.download(item.Name)
		if !ok {
			return "", r.canceled(item)
		}
		// Progress reaches every local user, so it names the item; the URL,
		// which can carry signed-query tokens, stays in the debug log.
		slog.Debug("Downloading", "item", item.DisplayName, "url", itemURL)
		r.emit(item, "downloading", percent, item.DisplayName)
		valid := download.IfNeeded(ctx, absFile, itemURL, installerItem.Hash)
		withdrawn := ctx.Err() != nil // read before done(), which cancels ctx
		done()
		if withdrawn {
			return "", r.canceled(item)
		}
		if !valid {
			err := fmt.Errorf("unable to download a valid installer for %s", item.DisplayName)
			slog.Warn("Unable to download valid file", "item", item.DisplayName)
			r.emit(item, "failed", percent, err.Error())
			return "", r.recordFailure(item, action, err)
		}
		percent = 50
	}

	// Build the command via the type implementation
	var cmd string
	var args []string
	var err error
	if action == "uninstall" {
		cmd, args, err = impl.uninstallCommand(item, absFile)
	} else {
		cmd, args, err = impl.installCommand(item, absFile)
	}
	if err != nil {
		slog.Warn("Unable to build command", "item", item.DisplayName, "installerType", installerItem.Type, "err", err)
		r.emit(item, "failed", percent, err.Error())
		return "", r.recordFailure(item, action, err)
	}

	// The last moment the user can withdraw the item; past this a cancel is refused.
	if !r.Cancels.act(item.Name) {
		return "", r.canceled(item)
	}

	// Run the command
	r.emit(item, state, percent, item.DisplayName)
	slog.Info(verb+"ing", "item", item.DisplayName, "version", item.Version, "installerType", installerItem.Type)
	out, err := runCommand(cmd, args)
	if err != nil {
		slog.Warn(verb+"ation FAILED", "item", item.DisplayName, "version", item.Version, "result", "error", "err", err)
		r.emit(item, "failed", percent, err.Error())
		return out, r.recordFailure(item, action, err)
	}
	slog.Info(verb+"ation SUCCESSFUL", "item", item.DisplayName, "version", item.Version, "result", "success")
	// The command changed the registry, so later checks in this run (the
	// self-serve prune, updates, the inventory) must not read the old snapshot.
	r.Checker.Invalidate()

	// Add the item to the report only after the command succeeded
	if action == "uninstall" {
		r.Report.UninstalledItems = append(r.Report.UninstalledItems, item)
	} else {
		r.Report.InstalledItems = append(r.Report.InstalledItems, item)
	}
	r.emit(item, "done", 100, "")

	return out, nil
}

func (r *Runner) installItem(item catalog.Item, itemURL string) (string, error) {
	return r.actionItem(item, itemURL, "install")
}

func (r *Runner) uninstallItem(item catalog.Item, itemURL string) (string, error) {
	return r.actionItem(item, itemURL, "uninstall")
}

// runScript writes the script to a unique temporary .ps1 under cachePath,
// executes it, and removes it. kind distinguishes the temp file names.
func runScript(script, kind, cachePath string) error {
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		return err
	}

	// Write the script to disk as a Powershell file
	tmpFile, err := os.CreateTemp(cachePath, "gorilla-"+kind+"-*.ps1")
	if err != nil {
		return err
	}
	tmpScript := tmpFile.Name()
	defer func() {
		if removeErr := os.Remove(tmpScript); removeErr != nil && !os.IsNotExist(removeErr) {
			slog.Warn("Unable to remove temporary script", "path", tmpScript, "err", removeErr)
		}
	}()
	_, writeErr := tmpFile.WriteString(script)
	closeErr := tmpFile.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}

	// Execute the script
	psArgs := []string{"-NoProfile", "-NoLogo", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", tmpScript}
	cmd := execCommand(commandPs1, psArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	// Log results
	slog.Debug("Script results", "err", err, "stdout", stdout.String(), "stderr", stderr.String())

	return err
}

var (
	// By putting the functions in a variable, we can override later in tests
	installItemFunc   = (*Runner).installItem
	uninstallItemFunc = (*Runner).uninstallItem
)

// Install determines if action needs to be taken on a item and then
// calls the appropriate function to install or uninstall
func (r *Runner) Install(item catalog.Item, installerType string) (string, error) {
	// Check the status and determine if any action is needed for this item
	actionNeeded, err := statusCheckStatus(r.Checker, item, installerType, r.CachePath)
	if err != nil {
		// installerType is the action selector (install/update/uninstall), so
		// it is the honest Action value for the report.
		return "", r.recordFailure(item, installerType, fmt.Errorf("unable to check status: %w", err))
	}

	// If no action is needed, record it so the inventory can report the item
	// as already installed (or already absent) rather than unknown
	if !actionNeeded {
		r.Report.NoActionItems = append(r.Report.NoActionItems, report.NoActionItem{
			Name:    item.Name,
			Version: item.Version,
			Action:  installerType,
		})
		return "Item not needed", nil
	}

	// A withdrawn item is skipped before anything runs for it, pre-scripts included.
	if r.Cancels.withdrawn(item.Name) {
		return "", r.canceled(item)
	}

	// Install or uninstall the item
	switch installerType {
	case "install", "update":
		// Check if checkonly mode is enabled
		if r.CheckOnly {
			r.Report.InstalledItems = append(r.Report.InstalledItems, item)
			slog.Info("[CHECK ONLY] Skipping actions", "item", item.DisplayName)
			// Check only mode doesn't perform any action, return
			return "Check only enabled", nil
		}

		// Defer instead of acting if a blocking app is running (R6)
		if err := r.blockingGate(item, installerType); err != nil {
			return "", err
		}

		// Compile the item's URL
		itemURL := r.URLPackages + item.Installer.Location

		// Run PreInstall_Script if needed
		if item.PreScript != "" {
			slog.Info("Running Pre-Install script", "item", item.DisplayName)
			if err := runScript(item.PreScript, "preinstall", r.CachePath); err != nil {
				return "", r.recordFailure(item, "install", fmt.Errorf("pre-install script error: %w", err))
			}
		}

		// Run the installer
		out, err := installItemFunc(r, item, itemURL)
		if err != nil {
			return out, err
		}

		// Run PostInstall_Script if needed
		if item.PostScript != "" {
			slog.Info("Running Post-Install script", "item", item.DisplayName)
			// Deliberate double record: the item genuinely installed
			// (InstalledItems) and the post-script genuinely failed (FailedItems).
			if err := runScript(item.PostScript, "postinstall", r.CachePath); err != nil {
				return out, r.recordFailure(item, "install", fmt.Errorf("post-install script error: %w", err))
			}
		}
		return out, nil

	case "uninstall":
		if r.CheckOnly {
			r.Report.InstalledItems = append(r.Report.InstalledItems, item)
			slog.Info("[CHECK ONLY] Skipping actions", "item", item.DisplayName)
			// Check only mode doesn't perform any action, return
			return "Check only enabled", nil
		}

		// Defer instead of acting if a blocking app is running (R6)
		if err := r.blockingGate(item, installerType); err != nil {
			return "", err
		}

		// Compile the item's URL
		itemURL := r.URLPackages + item.Uninstaller.Location

		// Run PreUninstall_Script if needed
		if item.PreUninstallScript != "" {
			slog.Info("Running Pre-Uninstall script", "item", item.DisplayName)
			if err := runScript(item.PreUninstallScript, "preuninstall", r.CachePath); err != nil {
				return "", r.recordFailure(item, "uninstall", fmt.Errorf("pre-uninstall script error: %w", err))
			}
		}

		// Run the uninstaller
		out, err := uninstallItemFunc(r, item, itemURL)
		if err != nil {
			return out, err
		}

		// Run PostUninstall_Script if needed
		if item.PostUninstallScript != "" {
			slog.Info("Running Post-Uninstall script", "item", item.DisplayName)
			// Deliberate double record: the item genuinely uninstalled
			// (UninstalledItems) and the post-script genuinely failed (FailedItems).
			if err := runScript(item.PostUninstallScript, "postuninstall", r.CachePath); err != nil {
				return out, r.recordFailure(item, "uninstall", fmt.Errorf("post-uninstall script error: %w", err))
			}
		}
		return out, nil

	default:
		// Programmer-error guard: process only ever passes install/update/uninstall,
		// so this arm is deliberately exempt from report recording.
		return "", fmt.Errorf("unsupported item type %q for %s", installerType, item.DisplayName)
	}
}
