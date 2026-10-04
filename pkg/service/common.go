package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/1dustindavis/gorilla/pkg/branding"
	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/download"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/report"
	"github.com/1dustindavis/gorilla/pkg/status"
)

var (
	manifestGet = manifest.Get
	catalogGet  = manifest.GetCatalogs
)

type Command struct {
	Action string   `json:"action"`
	Items  []string `json:"items,omitempty"`

	progress installer.ProgressFn
	// cancels is the service's withdrawal bookkeeping, set by the service for
	// every command it executes; nil from the command line.
	cancels *installer.Cancels
}

type CommandResponse struct {
	Status        string                `json:"status"`
	Message       string                `json:"message,omitempty"`
	Items         []string              `json:"items,omitempty"`
	OptionalItems []OptionalInstallItem `json:"optionalItems,omitempty"`
	OperationID   string                `json:"operationId,omitempty"`
	Branding      *branding.Branding    `json:"branding,omitempty"`

	// displayName is the catalog display name InstallItem resolved while
	// authorizing, for the operation's first record. Unexported like report.
	displayName string

	// prior is the self-serve selection an InstallItem or RemoveItem replaced and
	// requested the one it set; CancelOperation reverts from one to the other.
	prior, requested selection

	// report carries the managed run's per-run report from an actionRun back to
	// the caller so scheduleRunAfterMutation can emit an honest terminal event
	// (R10). Unexported so it is skipped by JSON and never reaches a client.
	report *report.Report
}

const (
	actionRun                   = "run"
	actionGetServiceInfo        = "GetServiceInfo"
	actionListOptionalInstalls  = "ListOptionalInstalls"
	actionGetBranding           = "GetBranding"
	actionInstallItem           = "InstallItem"
	actionRemoveItem            = "RemoveItem"
	actionStreamOperationStatus = "StreamOperationStatus"
	actionCancelOperation       = "CancelOperation"
)

func canonicalizeAction(action string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case strings.ToLower(actionRun):
		return actionRun, true
	case strings.ToLower(actionGetServiceInfo):
		return actionGetServiceInfo, true
	case strings.ToLower(actionListOptionalInstalls):
		return actionListOptionalInstalls, true
	case strings.ToLower(actionGetBranding):
		return actionGetBranding, true
	case strings.ToLower(actionInstallItem):
		return actionInstallItem, true
	case strings.ToLower(actionRemoveItem):
		return actionRemoveItem, true
	case strings.ToLower(actionStreamOperationStatus):
		return actionStreamOperationStatus, true
	case strings.ToLower(actionCancelOperation):
		return actionCancelOperation, true
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
	case actionGetServiceInfo, actionListOptionalInstalls, actionGetBranding:
		if len(cmd.Items) != 0 {
			return fmt.Errorf("%s action does not support items", cmd.Action)
		}
	case actionInstallItem, actionRemoveItem, actionStreamOperationStatus, actionCancelOperation:
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

	client := NewClient(cfg.ServicePipeName)
	ctx := context.Background()
	switch cmd.Action {
	case actionGetServiceInfo:
		info, err := client.GetServiceInfo(ctx)
		if err != nil {
			return CommandResponse{}, err
		}
		line, err := json.Marshal(info)
		if err != nil {
			return CommandResponse{}, fmt.Errorf("failed to encode service info: %w", err)
		}
		return CommandResponse{Status: "ok", Items: []string{string(line)}}, nil
	case actionListOptionalInstalls:
		items, err := client.ListOptionalInstalls(ctx)
		if err != nil {
			return CommandResponse{}, err
		}
		resp := CommandResponse{Status: "ok", OptionalItems: items, Items: make([]string, 0, len(items))}
		for _, item := range items {
			line, err := json.Marshal(item)
			if err != nil {
				return CommandResponse{}, fmt.Errorf("failed to encode optional item %q: %w", item.ItemName, err)
			}
			resp.Items = append(resp.Items, string(line))
		}
		return resp, nil
	case actionGetBranding:
		b, err := client.GetBranding(ctx)
		if err != nil {
			return CommandResponse{}, err
		}
		line, err := brandingSummary(b)
		if err != nil {
			return CommandResponse{}, err
		}
		return CommandResponse{Status: "ok", Branding: &b, Items: []string{line}}, nil
	case actionInstallItem:
		accepted, err := client.InstallItem(ctx, cmd.Items[0])
		if err != nil {
			return CommandResponse{}, err
		}
		return CommandResponse{Status: "ok", OperationID: accepted.OperationID}, nil
	case actionRemoveItem:
		accepted, err := client.RemoveItem(ctx, cmd.Items[0])
		if err != nil {
			return CommandResponse{}, err
		}
		return CommandResponse{Status: "ok", OperationID: accepted.OperationID}, nil
	case actionStreamOperationStatus:
		resp := CommandResponse{Status: "ok", Message: "StreamOperationStatus acknowledged by service"}
		err := client.StreamOperationStatus(ctx, cmd.Items[0], func(status OperationStatus) error {
			line, err := json.Marshal(status)
			if err != nil {
				return fmt.Errorf("failed to encode operation event: %w", err)
			}
			resp.Items = append(resp.Items, string(line))
			return nil
		})
		return resp, err
	case actionCancelOperation:
		if err := client.CancelOperation(ctx, cmd.Items[0]); err != nil {
			return CommandResponse{}, err
		}
		return CommandResponse{Status: "ok", OperationID: cmd.Items[0], Message: "operation canceled"}, nil
	default:
		return CommandResponse{}, fmt.Errorf("unsupported service action %q", cmd.Action)
	}
}

// brandingSummary is the one `-S GetBranding` output line: the payload with
// logoBase64 swapped for the logo's decoded size, so a console stays readable.
func brandingSummary(b branding.Branding) (string, error) {
	logo, err := base64.StdEncoding.DecodeString(b.LogoBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode branding logo: %w", err)
	}
	line, err := json.Marshal(struct {
		Title     string `json:"title"`
		Tagline   string `json:"tagline"`
		HelpURL   string `json:"helpUrl"`
		HelpLabel string `json:"helpLabel"`
		Accent    string `json:"accent"`
		LogoMime  string `json:"logoMime"`
		LogoBytes int    `json:"logoBytes"`
	}{b.Title, b.Tagline, b.HelpURL, b.HelpLabel, b.Accent, b.LogoMime, len(logo)})
	return string(line), err
}

func serviceInstallArgs(configPath string) []string {
	return []string{"-c", configPath, "-service"}
}

func executeCommand(cfg config.Configuration, cmd Command, managedRun func(config.Configuration, installer.ProgressFn, *installer.Cancels) (*report.Report, error)) (CommandResponse, error) {
	switch cmd.Action {
	case actionRun:
		defer cmd.cancels.EndRun()
		rep, err := managedRun(cfg, cmd.progress, cmd.cancels)
		return CommandResponse{Status: "ok", report: rep}, err
	case actionInstallItem:
		displayName, prior, err := addServiceManagedInstall(cfg, cmd.Items[0])
		if err != nil {
			return CommandResponse{}, err
		}
		// A new request starts the item's cancel bookkeeping afresh.
		cmd.cancels.Reset(cmd.Items[0])
		operationID := strconv.FormatInt(time.Now().UnixNano(), 10)
		return CommandResponse{Status: "ok", OperationID: operationID, displayName: displayName, prior: prior, requested: selectedForInstall}, nil
	case actionRemoveItem:
		prior, err := removeServiceManagedInstall(cfg, cmd.Items[0])
		if err != nil {
			return CommandResponse{}, err
		}
		cmd.cancels.Reset(cmd.Items[0])
		operationID := strconv.FormatInt(time.Now().UnixNano(), 10)
		return CommandResponse{Status: "ok", OperationID: operationID, prior: prior, requested: selectedForRemoval}, nil
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
	case actionGetBranding:
		b := branding.Resolve(cfg.Branding)
		return CommandResponse{Status: "ok", Branding: &b}, nil
	default:
		return CommandResponse{}, fmt.Errorf("unsupported service action %q", cmd.Action)
	}
}

func serviceLocalManifestPath(cfg config.Configuration) string {
	return manifest.SelfServePath(cfg.AppDataPath)
}

// selection is where one item sits in the self-serve manifest. InstallItem and
// RemoveItem each set an item's selection outright, so the exact inverse of
// either is to set back the selection it replaced.
type selection struct {
	install   bool // listed in managed_installs
	uninstall bool // listed in managed_uninstalls
}

var (
	// Selecting an item also drops a pending removal; Munki keeps
	// managed_installs and managed_uninstalls disjoint.
	selectedForInstall = selection{install: true}
	// Deselecting drives a real removal: the item is queued in
	// managed_uninstalls so the next run uninstalls it (R3).
	selectedForRemoval = selection{uninstall: true}
)

// setSelection sets name's selection in the self-serve manifest and returns
// the selection it replaced. Both lists stay deduplicated and sorted.
func setSelection(cfg config.Configuration, name string, want selection) (selection, error) {
	var prior selection
	err := manifest.UpdateSelfServe(serviceLocalManifestPath(cfg), func(entry *manifest.Item) bool {
		prior = selection{
			install:   slices.Contains(entry.Installs, name),
			uninstall: slices.Contains(entry.Uninstalls, name),
		}
		entry.Installs = setMember(entry.Installs, name, want.install)
		entry.Uninstalls = setMember(entry.Uninstalls, name, want.uninstall)
		return prior != want
	})
	return prior, err
}

func setMember(list []string, name string, member bool) []string {
	if !member {
		return slices.DeleteFunc(list, func(existing string) bool { return existing == name })
	}
	if slices.Contains(list, name) {
		return list
	}
	list = append(list, name)
	slices.Sort(list)
	return list
}

// addServiceManagedInstall selects name for install. It returns name's catalog
// display name, which the authorization lookup has already resolved, and the
// selection it replaced.
func addServiceManagedInstall(cfg config.Configuration, name string) (string, selection, error) {
	// Authorize the requested name against the currently available optional
	// installs before writing anything (R3). An unknown name is rejected and the
	// file is left untouched; the service answers item_not_available.
	available, err := getOptionalItems(cfg)
	if err != nil {
		return "", selection{}, err
	}
	i := slices.IndexFunc(available, func(it OptionalInstallItem) bool { return it.ItemName == name })
	if i < 0 {
		return "", selection{}, fmt.Errorf("%w: %q", errItemNotAvailable, name)
	}
	prior, err := setSelection(cfg, name, selectedForInstall)
	return available[i].DisplayName, prior, err
}

// removeServiceManagedInstall deselects name and queues its removal, returning
// the selection it replaced.
func removeServiceManagedInstall(cfg config.Configuration, name string) (selection, error) {
	return setSelection(cfg, name, selectedForRemoval)
}

var (
	// errNotCancelable is cancelOperation's refusal, sent as operation_not_cancelable.
	errNotCancelable = errors.New("operation can no longer be canceled")
	// errItemNotAvailable refuses an installItem for an item not offered for
	// self-service, sent as item_not_available.
	errItemNotAvailable = errors.New("item is not available for self-service")
)

// withdrawItem is the item side of CancelOperation. It puts back the selection
// the operation's request replaced (prior), so no later run performs it, then
// withdraws the item from any run under way. If a run started acting on the
// item first, it restores the request's own selection and returns
// errNotCancelable.
func withdrawItem(cfg config.Configuration, cancels *installer.Cancels, name string, prior, requested selection) error {
	if _, err := setSelection(cfg, name, prior); err != nil {
		return err
	}
	if cancels.Cancel(name) {
		return nil
	}
	if _, err := setSelection(cfg, name, requested); err != nil {
		slog.Warn("unable to restore the self-service selection after a refused cancel", "item", name, "err", err)
	}
	return errNotCancelable
}

func loadServiceLocalManifest(cfg config.Configuration) (manifest.Item, error) {
	return manifest.LoadSelfServe(serviceLocalManifestPath(cfg))
}

// getOptionalItems builds the honest ListOptionalInstalls payload (R9): it
// fetches the admin manifests and catalogs, loads the self-serve manifest, and
// for every offered optional name resolves the catalog metadata and real
// install status. The list call must stand on its own, so it seeds download's
// config rather than relying on a prior run.
func getOptionalItems(cfg config.Configuration) ([]OptionalInstallItem, error) {
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

	items := make([]OptionalInstallItem, 0, len(names))
	for _, name := range names {
		item := OptionalInstallItem{
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
		item.Description = catItem.Description
		item.Category = catItem.Category
		item.Developer = catItem.Developer
		item.IconName = catItem.IconName
		item.RestartAction = catItem.RestartAction

		// Script-only checks are not run on a list call (R9/OQ-C4): report Unknown.
		if catItem.Check.Script != "" &&
			len(catItem.Check.File) == 0 &&
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
// catalog index (1-based, as manifest.GetCatalogs keys them) to its configured name.
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
