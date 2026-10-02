// Package catalog declares the catalog schema shared by the agent and the
// repository tools. Keep it free of agent dependencies (config, download).
package catalog

// Item contains an individual entry from the catalog
type Item struct {
	Name                string        `yaml:"-"`
	Dependencies        []string      `yaml:"dependencies,omitempty"`
	DisplayName         string        `yaml:"display_name,omitempty"`
	Check               InstallCheck  `yaml:"check,omitempty"`
	Installer           InstallerItem `yaml:"installer,omitempty"`
	Uninstaller         InstallerItem `yaml:"uninstaller,omitempty"`
	Version             string        `yaml:"version,omitempty"`
	BlockingApps        []string      `yaml:"blocking_apps,omitempty"`
	UpdateFor           []string      `yaml:"update_for,omitempty"`
	Description         string        `yaml:"description,omitempty"`
	Category            string        `yaml:"category,omitempty"`
	Developer           string        `yaml:"developer,omitempty"`
	IconName            string        `yaml:"icon_name,omitempty"`
	RestartAction       string        `yaml:"restart_action,omitempty"`
	PreScript           string        `yaml:"preinstall_script,omitempty"`
	PostScript          string        `yaml:"postinstall_script,omitempty"`
	PreUninstallScript  string        `yaml:"preuninstall_script,omitempty"`
	PostUninstallScript string        `yaml:"postuninstall_script,omitempty"`
}

// InstallerItem holds information about how to install a catalog item
type InstallerItem struct {
	Type      string   `yaml:"type,omitempty"`
	Location  string   `yaml:"location,omitempty"`
	Hash      string   `yaml:"hash,omitempty"`
	PackageID string   `yaml:"package_id,omitempty"`
	Arguments []string `yaml:"arguments,omitempty"`
}

// InstallCheck holds information about how to check the status of a catalog item
type InstallCheck struct {
	File     []FileCheck `yaml:"file,omitempty"`
	Script   string      `yaml:"script,omitempty"`
	Registry RegCheck    `yaml:"registry,omitempty"`
	Appx     AppxCheck   `yaml:"appx,omitempty"`
}

// FileCheck holds information about checking via a file
type FileCheck struct {
	Path        string `yaml:"path,omitempty"`
	Version     string `yaml:"version,omitempty"`
	ProductName string `yaml:"product_name,omitempty"`
	Hash        string `yaml:"hash,omitempty"`
}

// RegCheck holds information about checking via registry
type RegCheck struct {
	Name    string `yaml:"name,omitempty"`
	Version string `yaml:"version,omitempty"`
}

// AppxCheck holds information about checking an installed AppX/MSIX package
type AppxCheck struct {
	Name    string `yaml:"name,omitempty"`
	Version string `yaml:"version,omitempty"`
}
