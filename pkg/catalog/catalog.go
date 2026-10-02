// Package catalog declares the catalog schema shared by the agent and the
// repository tools. Keep it free of agent dependencies (config, download).
package catalog

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
