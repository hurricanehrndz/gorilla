package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/download"
	"github.com/1dustindavis/gorilla/pkg/gorillalog"
	"github.com/1dustindavis/gorilla/pkg/installer"
	"github.com/1dustindavis/gorilla/pkg/manifest"
	"github.com/1dustindavis/gorilla/pkg/process"
	"github.com/1dustindavis/gorilla/pkg/report"
	"github.com/1dustindavis/gorilla/pkg/status"
)

var (
	adminCheckFunc = adminCheck
	mkdirAllFunc   = os.MkdirAll
	newReportFunc  = report.New
)

func managedRun(cfg config.Configuration, progress installer.ProgressFn) (_ *report.Report, runErr error) {
	// If not check-only, we need to run adminCheck().
	if !cfg.CheckOnly {
		admin, err := adminCheckFunc()
		if err != nil {
			return nil, fmt.Errorf("unable to check if running as admin: %w", err)
		}
		if !admin {
			return nil, errors.New("gorilla requires admnisistrative access. Please run as an administrator")
		}
	}

	// If needed, create the cache directory.
	if err := mkdirAllFunc(filepath.Clean(cfg.CachePath), 0o755); err != nil {
		return nil, fmt.Errorf("unable to create cache directory: %w", err)
	}

	// Create a new logger object
	if err := gorillalog.NewLog(cfg); err != nil {
		return nil, fmt.Errorf("unable to initialize logger: %w", err)
	}

	// Build the run-scoped state: report + status checker (K7)
	run := newReportFunc()

	// The inventory is finished even when the run fails part way, so osquery
	// sees the failure; plan collects every item the run considers.
	plan := newPlanBuilder()
	start := time.Now()
	defer func() { finishInventory(cfg, run, plan, start, runErr) }()

	// Set the configuration that `download` will use
	download.SetConfig(cfg)

	// Get the manifests
	slog.Info("Retrieving manifest", "manifest", cfg.Manifest)
	manifests, newCatalogs, err := manifest.Get(cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve manifest: %w", err)
	}

	// If we have newCatalogs, add them to the configuration
	if newCatalogs != nil {
		cfg.Catalogs = append(cfg.Catalogs, newCatalogs...)
	}

	// Get the catalogs
	slog.Info("Retrieving catalog", "catalogs", cfg.Catalogs)
	catalogs, err := manifest.GetCatalogs(cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve catalog: %w", err)
	}

	// Process the manifests into install type groups
	slog.Info("Processing manifest...")
	installs, uninstalls, updates := process.Manifests(manifests, catalogs)
	plan.catalogs = catalogs
	plan.add(report.KindManagedInstall, false, installs...)
	plan.add(report.KindManagedUninstall, false, uninstalls...)
	plan.add(report.KindManagedUpdate, false, updates...)

	// Reconcile the self-serve manifest: assert once-only defaults, authorize
	// user selections against the admin optional_installs, and queue deselected
	// items for removal (R2, R4, R5).
	selfServePath := manifest.SelfServePath(cfg.AppDataPath)
	selfServe, err := manifest.LoadSelfServe(selfServePath)
	if err != nil {
		return nil, fmt.Errorf("unable to load self-serve manifest: %w", err)
	}
	ssInstalls, ssUninstalls, changed := process.ReconcileSelfServe(&selfServe, manifests)
	if changed {
		// Defaults are asserted on every run, including check-only (Munki asserts
		// during updatecheck), so save regardless of CheckOnly.
		if err := manifest.SaveSelfServe(selfServePath, selfServe); err != nil {
			return nil, fmt.Errorf("unable to save self-serve manifest: %w", err)
		}
	}
	installs = append(installs, ssInstalls...)
	uninstalls = append(uninstalls, ssUninstalls...)
	plan.add(report.KindManagedUninstall, true, ssUninstalls...)
	plan.addSelfServe(ssInstalls, selfServe)

	// Build the run-scoped installer context (K7)
	runner := &installer.Runner{
		Report:      run,
		Checker:     &status.Checker{},
		Emit:        progress,
		URLPackages: cfg.URLPackages,
		CachePath:   cfg.CachePath,
		CheckOnly:   cfg.CheckOnly,
	}

	// Expand update_for (R7): build the updater index once, ride updaters of
	// referents merely installed on disk into the install list (referents being
	// installed this run are expanded in-walk), and couple updater removals to
	// their referent's removal.
	index := process.UpdaterIndex(catalogs)
	installsSet := make(map[string]bool, len(installs))
	for _, name := range installs {
		installsSet[name] = true
	}
	riders := process.InstalledReferentUpdaters(catalogs, index, installsSet, runner.Checker, cfg.CachePath)
	installs = append(installs, riders...)
	uninstalls = process.ExpandUninstallsWithUpdaters(uninstalls, index)
	plan.add(report.KindUpdateFor, false, riders...)
	plan.add(report.KindManagedUninstall, false, uninstalls...)
	plan.addUpdaters(index)

	// Prepare and install
	slog.Info("Processing managed installs...")
	process.Installs(installs, catalogs, runner, index)

	// Prepare and uninstall
	slog.Info("Processing managed uninstalls...")
	process.Uninstalls(uninstalls, catalogs, runner)

	// Prune self-serve uninstalls that are confirmed gone so the user can
	// reinstall later (R5). Only after a real run, never in check-only.
	if !cfg.CheckOnly {
		if process.PruneSelfServeUninstalls(&selfServe, catalogs, runner.Checker, cfg.CachePath) {
			if err := manifest.SaveSelfServe(selfServePath, selfServe); err != nil {
				return nil, fmt.Errorf("unable to save self-serve manifest after prune: %w", err)
			}
		}
	}

	// Prepare and update
	slog.Info("Processing managed updates...")
	process.Updates(updates, catalogs, runner)
	plan.checkUpdates(run, runner.Checker, cfg.CachePath)

	// Offered optional installs the user has not selected still belong in the
	// inventory; the deferred finishInventory saves or prints it.
	plan.addAvailable(manifests, runner.Checker, cfg.CachePath)

	// Run CleanUp to delete old cached items and empty directories
	slog.Info("Cleaning up the cache...")
	process.CleanUp(cfg.CachePath)

	slog.Info("Done!")
	return run, nil
}
