package catalog

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/1dustindavis/gorilla/pkg/config"
	"github.com/1dustindavis/gorilla/pkg/download"
	"go.yaml.in/yaml/v4"
)

// Item contains an individual entry from the catalog
type Item struct {
	Name                string        `yaml:"-"`
	Dependencies        []string      `yaml:"dependencies"`
	DisplayName         string        `yaml:"display_name"`
	Check               InstallCheck  `yaml:"check"`
	Installer           InstallerItem `yaml:"installer"`
	Uninstaller         InstallerItem `yaml:"uninstaller"`
	Version             string        `yaml:"version"`
	BlockingApps        []string      `yaml:"blocking_apps"`
	UpdateFor           []string      `yaml:"update_for"`
	Description         string        `yaml:"description"`
	Category            string        `yaml:"category"`
	Developer           string        `yaml:"developer"`
	IconName            string        `yaml:"icon_name"`
	RestartAction       string        `yaml:"restart_action"`
	PreScript           string        `yaml:"preinstall_script"`
	PostScript          string        `yaml:"postinstall_script"`
	PreUninstallScript  string        `yaml:"preuninstall_script"`
	PostUninstallScript string        `yaml:"postuninstall_script"`
}

// InstallerItem holds information about how to install a catalog item
type InstallerItem struct {
	Type      string   `yaml:"type"`
	Location  string   `yaml:"location"`
	Hash      string   `yaml:"hash"`
	PackageID string   `yaml:"package_id"`
	Arguments []string `yaml:"arguments"`
}

// InstallCheck holds information about how to check the status of a catalog item
type InstallCheck struct {
	File     []FileCheck `yaml:"file"`
	Script   string      `yaml:"script"`
	Registry RegCheck    `yaml:"registry"`
	Appx     AppxCheck   `yaml:"appx"`
}

// FileCheck holds information about checking via a file
type FileCheck struct {
	Path        string `yaml:"path"`
	Version     string `yaml:"version"`
	ProductName string `yaml:"product_name"`
	Hash        string `yaml:"hash"`
}

// RegCheck holds information about checking via registry
type RegCheck struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// AppxCheck holds information about checking an installed AppX/MSIX package
type AppxCheck struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// This abstraction allows us to override the function while testing
var downloadGet = download.Get

// Get returns a map of `Item` from the catalog and any fatal catalog-loading error.
func Get(cfg config.Configuration) (map[int]map[string]Item, error) {
	// catalogMap is an map of parsed catalogs
	catalogMap := make(map[int]map[string]Item)

	// catalogCount allows us to be sure we are processing catalogs in order
	catalogCount := 0

	// Error if dont have at least one catalog
	if len(cfg.Catalogs) < 1 {
		return nil, errors.New("unable to continue, no catalogs assigned")
	}

	// Loop through the catalogs and get each one in order
	for _, catalog := range cfg.Catalogs {

		// Download the catalog
		catalogURL := cfg.URL + "catalogs/" + catalog + ".yaml"
		slog.Info("Catalog Url", "url", catalogURL)
		yamlFile, err := downloadGet(catalogURL)
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve catalog %s: %w", catalogURL, err)
		}

		// Parse the catalog
		var catalogItems map[string]Item
		err = yaml.Unmarshal(yamlFile, &catalogItems)
		if err != nil {
			return nil, fmt.Errorf("unable to parse yaml catalog %s: %w", catalogURL, err)
		}

		catalogCount++

		// Stamp each item with its catalog map key so items know their own name (R13)
		for name, item := range catalogItems {
			item.Name = name
			catalogItems[name] = item
		}

		// Add the new parsed catalog items to the catalogMap
		catalogMap[catalogCount] = catalogItems
	}

	return catalogMap, nil
}
