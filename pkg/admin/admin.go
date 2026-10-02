package admin

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"go.yaml.in/yaml/v4"
)

type packageInfo struct {
	ItemName string       `yaml:"item_name"`
	Catalog  string       `yaml:"catalog"`
	Item     catalog.Item `yaml:",inline"`
}

// BuildCatalogs compiles package-info files from <repo>/packages-info into <repo>/catalogs.
func BuildCatalogs(repoPath string) error {
	packagesInfoPath := filepath.Join(repoPath, "packages-info")
	catalogsPath := filepath.Join(repoPath, "catalogs")

	if _, err := os.Stat(packagesInfoPath); err != nil {
		return fmt.Errorf("packages-info path unavailable: %w", err)
	}

	var packageInfoQueue []string
	err := filepath.WalkDir(packagesInfoPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			slog.Warn("Failed to access path", "path", path, "err", walkErr)
			return walkErr
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
		return err
	}

	catalogMap := make(map[string]map[string]catalog.Item)
	for _, packageInfoPath := range packageInfoQueue {
		yamlFile, err := os.ReadFile(packageInfoPath)
		if err != nil {
			return fmt.Errorf("read package-info %s: %w", packageInfoPath, err)
		}

		var parsed packageInfo
		if err = yaml.Unmarshal(yamlFile, &parsed); err != nil {
			return fmt.Errorf("parse package-info %s: %w", packageInfoPath, err)
		}
		if parsed.Catalog == "" {
			slog.Warn("Skipping package-info with no catalog", "path", packageInfoPath)
			continue
		}

		itemName := strings.TrimSpace(parsed.ItemName)
		if itemName == "" {
			itemName = strings.TrimSpace(strings.ReplaceAll(parsed.Item.DisplayName, " ", ""))
		}
		if itemName == "" {
			itemName = strings.TrimSuffix(filepath.Base(packageInfoPath), filepath.Ext(packageInfoPath))
		}
		if itemName == "" {
			slog.Warn("Skipping package-info with no item_name/display_name", "path", packageInfoPath)
			continue
		}

		if catalogMap[parsed.Catalog] == nil {
			catalogMap[parsed.Catalog] = map[string]catalog.Item{}
		}
		catalogMap[parsed.Catalog][itemName] = parsed.Item
	}

	if err := os.RemoveAll(catalogsPath); err != nil {
		return fmt.Errorf("clean catalogs path %s: %w", catalogsPath, err)
	}
	if err := os.MkdirAll(catalogsPath, 0o755); err != nil {
		return fmt.Errorf("create catalogs path %s: %w", catalogsPath, err)
	}

	for catalogName, catalogItems := range catalogMap {
		catalogYAML, err := yaml.Marshal(catalogItems)
		if err != nil {
			return fmt.Errorf("marshal catalog %s: %w", catalogName, err)
		}
		catalogPath := filepath.Join(catalogsPath, catalogName+".yaml")
		if err := os.WriteFile(catalogPath, catalogYAML, 0o644); err != nil {
			return fmt.Errorf("write catalog %s: %w", catalogPath, err)
		}
	}

	return nil
}
