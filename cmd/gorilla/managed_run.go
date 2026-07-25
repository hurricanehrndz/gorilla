package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/1dustindavis/gorilla/pkg/admin"
	"github.com/1dustindavis/gorilla/pkg/catalog"
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
	adminCheckFunc    = adminCheck
	mkdirAllFunc      = os.MkdirAll
	buildCatalogsFunc = admin.BuildCatalogs
	importItemFunc    = admin.ImportItem
	newReportFunc     = report.New
)

func managedRun(cfg config.Configuration, progress installer.ProgressFn) (*report.Report, error) {
	// Build/import modes operate on repo metadata and do not require admin.
	buildMode := cfg.BuildArg || cfg.ImportArg != ""

	// If not check-only and not build/import, we need to run adminCheck().
	if !cfg.CheckOnly && !buildMode {
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

	if cfg.BuildArg {
		slog.Info("Building catalogs...")
		if err := buildCatalogsFunc(cfg.RepoPath); err != nil {
			return nil, fmt.Errorf("error building catalogs: %w", err)
		}
		return nil, nil
	}

	if cfg.ImportArg != "" {
		slog.Info("Importing item...")
		if err := importItemFunc(cfg.RepoPath, cfg.ImportArg); err != nil {
			return nil, fmt.Errorf("error importing item: %w", err)
		}
		return nil, nil
	}

	// Build the run-scoped state: report + status checker (K7)
	run := newReportFunc()
	run.Items["Manifest"] = cfg.Manifest
	run.Items["Catalog"] = cfg.Catalogs

	// Start creating GorillaReport
	if !cfg.CheckOnly {
		run.Start()
		defer run.End()
	}

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
	catalogs, err := catalog.Get(cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve catalog: %w", err)
	}

	// Process the manifests into install type groups
	slog.Info("Processing manifest...")
	installs, uninstalls, updates := process.Manifests(manifests, catalogs)

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
	installs = append(installs, process.InstalledReferentUpdaters(catalogs, index, installsSet, runner.Checker, cfg.CachePath)...)
	uninstalls = process.ExpandUninstallsWithUpdaters(uninstalls, index)

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

	// Save GorillaReport to disk
	slog.Info("Saving GorillaReport.json...")
	if cfg.CheckOnly {
		run.Print()
	}

	// Run CleanUp to delete old cached items and empty directories
	slog.Info("Cleaning up the cache...")
	process.CleanUp(cfg.CachePath)

	slog.Info("Done!")
	return run, nil
}
