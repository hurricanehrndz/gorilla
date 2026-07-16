package service

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/download"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/report"
	"github.com/1dustindavis/gorilla/pkg/status"
)

var (
	manifestGet = manifest.Get
	catalogGet  = catalog.Get
)

type Command struct {
	Action string   `json:"action"`
	Items  []string `json:"items,omitempty"`
}

type CommandResponse struct {
	Status        string                        `json:"status"`
	Message       string                        `json:"message,omitempty"`
	Items         []string                      `json:"items,omitempty"`
	OptionalItems []optionalInstallResponseItem `json:"optionalItems,omitempty"`
	OperationID   string                        `json:"operationId,omitempty"`

	// report carries the managed run's per-run report from an actionRun back to
	// the caller so scheduleRunAfterMutation can emit an honest terminal event
	// (R10). Unexported so it is skipped by JSON and never crosses the pipe.
	report *report.Report
}

const (
	actionRun                   = "run"
	actionListOptionalInstalls  = "ListOptionalInstalls"
	actionInstallItem           = "InstallItem"
	actionRemoveItem            = "RemoveItem"
	actionStreamOperationStatus = "StreamOperationStatus"
)

func canonicalizeAction(action string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case strings.ToLower(actionRun):
		return actionRun, true
	case strings.ToLower(actionListOptionalInstalls):
		return actionListOptionalInstalls, true
	case strings.ToLower(actionInstallItem):
		return actionInstallItem, true
	case strings.ToLower(actionRemoveItem):
		return actionRemoveItem, true
	case strings.ToLower(actionStreamOperationStatus):
		return actionStreamOperationStatus, true
	default:
		return "", false
	}
}

func parseCommandSpec(spec string) (Command, error) {
	var cmd Command
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return cmd, errors.New("service command cannot be empty")
	}

	parts := strings.SplitN(spec, ":", 2)
	canonicalAction, ok := canonicalizeAction(parts[0])
	if !ok {
		return cmd, fmt.Errorf("unsupported service action %q", strings.TrimSpace(parts[0]))
	}
	cmd.Action = canonicalAction
	if len(parts) == 2 {
		items := strings.Split(parts[1], ",")
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item != "" {
				cmd.Items = append(cmd.Items, item)
			}
		}
	}
	return cmd, validateCommand(cmd)
}

func validateCommand(cmd Command) error {
	canonicalAction, ok := canonicalizeAction(cmd.Action)
	if !ok {
		return fmt.Errorf("unsupported service action %q", cmd.Action)
	}
	cmd.Action = canonicalAction

	switch cmd.Action {
	case actionRun:
		if len(cmd.Items) != 0 {
			return errors.New("run action does not support items")
		}
	case actionListOptionalInstalls:
		if len(cmd.Items) != 0 {
			return fmt.Errorf("%s action does not support items", cmd.Action)
		}
	case actionInstallItem, actionRemoveItem, actionStreamOperationStatus:
		if len(cmd.Items) != 1 {
			return fmt.Errorf("%s action requires exactly one argument", cmd.Action)
		}
	default:
		return fmt.Errorf("unsupported service action %q", cmd.Action)
	}

	return nil
}

func SendCommand(cfg config.Configuration, spec string) (CommandResponse, error) {
	cmd, err := parseCommandSpec(spec)
	if err != nil {
		return CommandResponse{}, err
	}
	return sendCommand(cfg, cmd)
}

func serviceInstallArgs(configPath string) []string {
	return []string{"-c", configPath, "-service"}
}

func executeCommand(cfg config.Configuration, cmd Command, managedRun func(config.Configuration) (*report.Report, error)) (CommandResponse, error) {
	switch cmd.Action {
	case actionRun:
		rep, err := managedRun(cfg)
		return CommandResponse{Status: "ok", report: rep}, err
	case actionInstallItem:
		if err := addServiceManagedInstalls(cfg, cmd.Items); err != nil {
			return CommandResponse{}, err
		}
		operationID := strconv.FormatInt(time.Now().UnixNano(), 10)
		return CommandResponse{Status: "ok", OperationID: operationID}, nil
	case actionRemoveItem:
		if err := removeServiceManagedInstalls(cfg, cmd.Items); err != nil {
			return CommandResponse{}, err
		}
		operationID := strconv.FormatInt(time.Now().UnixNano(), 10)
		return CommandResponse{Status: "ok", OperationID: operationID}, nil
	case actionListOptionalInstalls:
		items, err := getOptionalItems(cfg)
		if err != nil {
			return CommandResponse{}, err
		}
		names := make([]string, 0, len(items))
		for _, it := range items {
			names = append(names, it.ItemName)
		}
		return CommandResponse{Status: "ok", Items: names, OptionalItems: items}, nil
	case actionStreamOperationStatus:
		return CommandResponse{
			Status:  "ok",
			Message: "stream status is not yet implemented in the service",
		}, nil
	default:
		return CommandResponse{}, fmt.Errorf("unsupported service action %q", cmd.Action)
	}
}

func serviceLocalManifestPath(cfg config.Configuration) string {
	return manifest.SelfServePath(cfg.AppDataPath)
}

func addServiceManagedInstalls(cfg config.Configuration, items []string) error {
	// Authorize each requested name against the currently available optional
	// installs before writing anything (R3). An unknown name is rejected and the
	// file is left untouched; the pipe layer maps the error to an error envelope.
	available, err := getOptionalItems(cfg)
	if err != nil {
		return err
	}
	availableNames := make(map[string]bool, len(available))
	for _, it := range available {
		availableNames[it.ItemName] = true
	}
	for _, item := range items {
		if !availableNames[item] {
			return fmt.Errorf("item %q is not available for self-service", item)
		}
	}

	entry, err := loadServiceLocalManifest(cfg)
	if err != nil {
		return err
	}

	for _, item := range items {
		if !slices.Contains(entry.Installs, item) {
			entry.Installs = append(entry.Installs, item)
		}
		// Re-selecting an item pending removal cancels the removal; Munki keeps
		// managed_installs and managed_uninstalls disjoint.
		entry.Uninstalls = slices.DeleteFunc(entry.Uninstalls, func(u string) bool { return u == item })
	}
	slices.Sort(entry.Installs)

	return saveServiceLocalManifest(cfg, entry)
}

func removeServiceManagedInstalls(cfg config.Configuration, items []string) error {
	entry, err := loadServiceLocalManifest(cfg)
	if err != nil {
		return err
	}

	filtered := make([]string, 0, len(entry.Installs))
	for _, existing := range entry.Installs {
		if !slices.Contains(items, existing) {
			filtered = append(filtered, existing)
		}
	}
	entry.Installs = filtered

	// Deselecting drives a real removal: queue the item in managed_uninstalls
	// (dedup, sorted) so the next run uninstalls it (R3).
	for _, item := range items {
		if !slices.Contains(entry.Uninstalls, item) {
			entry.Uninstalls = append(entry.Uninstalls, item)
		}
	}
	slices.Sort(entry.Uninstalls)

	return saveServiceLocalManifest(cfg, entry)
}

func loadServiceLocalManifest(cfg config.Configuration) (manifest.Item, error) {
	return manifest.LoadSelfServe(serviceLocalManifestPath(cfg))
}

func saveServiceLocalManifest(cfg config.Configuration, entry manifest.Item) error {
	return manifest.SaveSelfServe(serviceLocalManifestPath(cfg), entry)
}

// getOptionalItems builds the honest ListOptionalInstalls payload (R9): it
// fetches the admin manifests and catalogs, loads the self-serve manifest, and
// for every offered optional name resolves the catalog metadata and real
// install status. The list call must stand on its own, so it seeds download's
// config rather than relying on a prior run.
func getOptionalItems(cfg config.Configuration) ([]optionalInstallResponseItem, error) {
	download.SetConfig(cfg)

	manifests, newCatalogs, err := manifestGet(cfg)
	if err != nil {
		return nil, err
	}
	if newCatalogs != nil {
		cfg.Catalogs = append(cfg.Catalogs, newCatalogs...)
	}

	catalogs, err := catalogGet(cfg)
	if err != nil {
		return nil, err
	}

	selfServe, err := manifest.LoadSelfServe(manifest.SelfServePath(cfg.AppDataPath))
	if err != nil {
		return nil, err
	}
	selected := sliceSet(selfServe.Installs)
	pendingRemoval := sliceSet(selfServe.Uninstalls)

	// Union of offered optional names, deduped and sorted for a stable payload.
	names := make([]string, 0)
	seen := make(map[string]bool)
	for _, m := range manifests {
		for _, name := range m.OptionalInstalls {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	slices.Sort(names)

	// One shared checker so the registry snapshot amortizes across items.
	checker := &status.Checker{}
	now := nowRFC3339UTC()

	items := make([]optionalInstallResponseItem, 0, len(names))
	for _, name := range names {
		item := optionalInstallResponseItem{
			ItemName:           name,
			DisplayName:        name,
			IsManaged:          selected[name],
			Status:             "Unknown",
			StatusUpdatedAtUTC: now,
		}

		catItem, catName, ok := firstCatalogItem(name, catalogs, cfg.Catalogs)
		if !ok {
			// No valid catalog item anywhere — still listed, status Unknown (R9).
			slog.Warn("optional item has no catalog entry", "item", name)
			items = append(items, item)
			continue
		}

		item.DisplayName = orDefault(catItem.DisplayName, name)
		item.Version = catItem.Version
		item.Catalog = catName
		item.InstallerType = catItem.Installer.Type
		item.InstallerLocation = catItem.Installer.Location
		item.InstallerPackageID = catItem.Installer.PackageID
		item.Description = catItem.Description
		item.Category = catItem.Category
		item.Developer = catItem.Developer
		item.IconName = catItem.IconName
		item.RestartAction = catItem.RestartAction

		// Script-only checks are not run on a list call (R9/OQ-C4): report Unknown.
		if catItem.Check.Script != "" &&
			catItem.Check.File == nil &&
			catItem.Check.Registry.Version == "" &&
			catItem.Check.Appx.Name == "" {
			items = append(items, item)
			continue
		}

		// CheckStatus(uninstall) reports true when the item is still installed.
		installed, checkErr := checker.CheckStatus(catItem, "uninstall", cfg.CachePath)
		if checkErr != nil {
			slog.Warn("unable to check optional item status", "item", name, "err", checkErr)
			items = append(items, item)
			continue
		}
		item.IsInstalled = installed
		switch {
		case installed && pendingRemoval[name]:
			item.Status = "WillBeRemoved"
		case installed:
			item.Status = "Installed"
		case item.IsManaged:
			item.Status = "WillBeInstalled"
		default:
			item.Status = "NotInstalled"
		}
		items = append(items, item)
	}
	return items, nil
}

// firstCatalogItem returns the first-catalog-wins catalog item for name and the
// name of the catalog it came from. Unlike process.firstItem it applies no
// installer-validity rules — the list is a display surface. catalogNames maps a
// catalog index (1-based, as catalog.Get keys them) to its configured name.
func firstCatalogItem(name string, catalogs map[int]map[string]catalog.Item, catalogNames []string) (catalog.Item, string, bool) {
	indexes := make([]int, 0, len(catalogs))
	for k := range catalogs {
		indexes = append(indexes, k)
	}
	slices.Sort(indexes)
	for _, k := range indexes {
		if item, ok := catalogs[k][name]; ok {
			catName := ""
			if idx := k - 1; idx >= 0 && idx < len(catalogNames) {
				catName = catalogNames[idx]
			}
			return item, catName, true
		}
	}
	return catalog.Item{}, "", false
}

func sliceSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
