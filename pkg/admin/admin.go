// Package admin holds repository-side tooling. It must stay free of agent
// dependencies (pkg/config, pkg/download) so cmd/makecatalogs cross-builds
// for any platform.
package admin

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"go.yaml.in/yaml/v4"
)

type packageInfo struct {
	ItemName string       `yaml:"item_name"`
	Catalog  string       `yaml:"catalog"`
	Item     catalog.Item `yaml:",inline"`
}

// CatalogSet is the result of compiling a repo's package-info files.
type CatalogSet struct {
	// Catalogs maps catalog name to item name to item.
	Catalogs map[string]map[string]catalog.Item
	// Problems lists package-info files that were skipped or overridden.
	// A normal makecatalogs run warns about them and carries on; --check fails on them.
	Problems []string
}

// Names returns the catalog names in sorted order.
func (s CatalogSet) Names() []string {
	names := make([]string, 0, len(s.Catalogs))
	for name := range s.Catalogs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CollectCatalogs reads <repo>/packages-info and compiles the catalogs it
// describes without writing anything. Unreadable or unparsable files are
// errors; skipped or duplicated items are reported in Problems.
func CollectCatalogs(repoPath string) (CatalogSet, error) {
	packagesInfoPath := filepath.Join(repoPath, "packages-info")

	if _, err := os.Stat(packagesInfoPath); err != nil {
		return CatalogSet{}, fmt.Errorf("packages-info path unavailable: %w", err)
	}

	var packageInfoQueue []string
	err := filepath.WalkDir(packagesInfoPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Skip dotfiles such as macOS AppleDouble `._foo.yaml`, as Munki does.
		if strings.HasPrefix(d.Name(), ".") && path != packagesInfoPath {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml":
			slog.Debug("Queuing package-info file", "path", path)
			packageInfoQueue = append(packageInfoQueue, path)
		}
		return nil
	})
	if err != nil {
		return CatalogSet{}, err
	}

	set := CatalogSet{Catalogs: make(map[string]map[string]catalog.Item)}
	// source records which package-info file each catalog item came from.
	source := make(map[string]map[string]string)
	// folded maps a lowercased catalog name to the first spelling seen, since
	// macOS and Windows file systems treat Prod.yaml and prod.yaml as one file.
	folded := make(map[string]string)
	for _, packageInfoPath := range packageInfoQueue {
		yamlFile, err := os.ReadFile(packageInfoPath)
		if err != nil {
			return CatalogSet{}, fmt.Errorf("read package-info %s: %w", packageInfoPath, err)
		}

		var parsed packageInfo
		if err = yaml.Unmarshal(yamlFile, &parsed); err != nil {
			return CatalogSet{}, fmt.Errorf("parse package-info %s: %w", packageInfoPath, err)
		}
		if parsed.Catalog == "" {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: no catalog; skipped", packageInfoPath))
			continue
		}
		// The catalog name becomes a file name under catalogs/, so it must not
		// carry a path.
		if strings.ContainsAny(parsed.Catalog, `/\:`) || parsed.Catalog == "." || parsed.Catalog == ".." {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: invalid catalog name %q; skipped", packageInfoPath, parsed.Catalog))
			continue
		}
		if first, ok := folded[strings.ToLower(parsed.Catalog)]; ok && first != parsed.Catalog {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: catalog %q differs from %q only by case; skipped", packageInfoPath, parsed.Catalog, first))
			continue
		}
		folded[strings.ToLower(parsed.Catalog)] = parsed.Catalog

		itemName := strings.TrimSpace(parsed.ItemName)
		if itemName == "" {
			itemName = strings.TrimSpace(strings.ReplaceAll(parsed.Item.DisplayName, " ", ""))
		}
		if itemName == "" {
			itemName = strings.TrimSuffix(filepath.Base(packageInfoPath), filepath.Ext(packageInfoPath))
		}
		if itemName == "" {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: no item_name/display_name; skipped", packageInfoPath))
			continue
		}

		if set.Catalogs[parsed.Catalog] == nil {
			set.Catalogs[parsed.Catalog] = map[string]catalog.Item{}
			source[parsed.Catalog] = map[string]string{}
		}
		if prev, ok := source[parsed.Catalog][itemName]; ok {
			set.Problems = append(set.Problems, fmt.Sprintf("%s: duplicate item %q in catalog %q; replaces %s", packageInfoPath, itemName, parsed.Catalog, prev))
		}
		set.Catalogs[parsed.Catalog][itemName] = parsed.Item
		source[parsed.Catalog][itemName] = packageInfoPath
	}

	// Writing would replace catalogs/ with nothing; refuse rather than publish
	// an empty repo.
	if len(set.Catalogs) == 0 {
		return CatalogSet{}, fmt.Errorf("no catalogs to write: no package-info file under %s names a valid catalog", packagesInfoPath)
	}

	return set, nil
}

// WriteCatalogs replaces <repo>/catalogs with one YAML file per catalog in set.
func WriteCatalogs(repoPath string, set CatalogSet) error {
	catalogsPath := filepath.Join(repoPath, "catalogs")

	// Marshal everything before touching the existing catalogs.
	rendered := make(map[string][]byte, len(set.Catalogs))
	for catalogName, catalogItems := range set.Catalogs {
		catalogYAML, err := yaml.Marshal(catalogItems)
		if err != nil {
			return fmt.Errorf("marshal catalog %s: %w", catalogName, err)
		}
		rendered[catalogName] = catalogYAML
	}

	// CEILING: not atomic. A write error after RemoveAll (disk full, file lock)
	// leaves catalogs/ partial; rerunning repairs it. Upgrade path: write to a
	// sibling temp dir and rename it into place.
	if err := os.RemoveAll(catalogsPath); err != nil {
		return fmt.Errorf("clean catalogs path %s: %w", catalogsPath, err)
	}
	if err := os.MkdirAll(catalogsPath, 0o755); err != nil {
		return fmt.Errorf("create catalogs path %s: %w", catalogsPath, err)
	}
	for catalogName, catalogYAML := range rendered {
		catalogPath := filepath.Join(catalogsPath, catalogName+".yaml")
		if err := os.WriteFile(catalogPath, catalogYAML, 0o644); err != nil {
			return fmt.Errorf("write catalog %s: %w", catalogPath, err)
		}
	}

	return nil
}
