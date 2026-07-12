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

func managedRun(cfg config.Configuration) error {
	// Build/import modes operate on repo metadata and do not require admin.
	buildMode := cfg.BuildArg || cfg.ImportArg != ""

	// If not check-only and not build/import, we need to run adminCheck().
	if !cfg.CheckOnly && !buildMode {
		admin, err := adminCheckFunc()
		if err != nil {
			return fmt.Errorf("unable to check if running as admin: %w", err)
		}
		if !admin {
			return errors.New("gorilla requires admnisistrative access. Please run as an administrator")
		}
	}

	// If needed, create the cache directory.
	if err := mkdirAllFunc(filepath.Clean(cfg.CachePath), 0o755); err != nil {
		return fmt.Errorf("unable to create cache directory: %w", err)
	}

	// Create a new logger object
	if err := gorillalog.NewLog(cfg); err != nil {
		return fmt.Errorf("unable to initialize logger: %w", err)
	}

	if cfg.BuildArg {
		slog.Info("Building catalogs...")
		if err := buildCatalogsFunc(cfg.RepoPath); err != nil {
			return fmt.Errorf("error building catalogs: %w", err)
		}
		return nil
	}

	if cfg.ImportArg != "" {
		slog.Info("Importing item...")
		if err := importItemFunc(cfg.RepoPath, cfg.ImportArg); err != nil {
			return fmt.Errorf("error importing item: %w", err)
		}
		return nil
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
		return fmt.Errorf("unable to retrieve manifest: %w", err)
	}

	// If we have newCatalogs, add them to the configuration
	if newCatalogs != nil {
		cfg.Catalogs = append(cfg.Catalogs, newCatalogs...)
	}

	// Get the catalogs
	slog.Info("Retrieving catalog", "catalogs", cfg.Catalogs)
	catalogs, err := catalog.Get(cfg)
	if err != nil {
		return fmt.Errorf("unable to retrieve catalog: %w", err)
	}

	// Process the manifests into install type groups
	slog.Info("Processing manifest...")
	installs, uninstalls, updates := process.Manifests(manifests, catalogs)

	// Build the run-scoped installer context (K7)
	runner := &installer.Runner{
		Report:      run,
		Checker:     &status.Checker{},
		URLPackages: cfg.URLPackages,
		CachePath:   cfg.CachePath,
		CheckOnly:   cfg.CheckOnly,
	}

	// Prepare and install
	slog.Info("Processing managed installs...")
	process.Installs(installs, catalogs, runner)

	// Prepare and uninstall
	slog.Info("Processing managed uninstalls...")
	process.Uninstalls(uninstalls, catalogs, runner)

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
	return nil
}
