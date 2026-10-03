package manifest

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"github.com/1dustindavis/gorilla/pkg/config"
	"go.yaml.in/yaml/v4"
)

// GetCatalogs returns a map of `catalog.Item` from the catalogs and any fatal catalog-loading error.
// It lives on the agent side so pkg/catalog stays a dependency-free schema package.
func GetCatalogs(cfg config.Configuration) (map[int]map[string]catalog.Item, error) {
	// catalogMap is an map of parsed catalogs
	catalogMap := make(map[int]map[string]catalog.Item)

	// catalogCount allows us to be sure we are processing catalogs in order
	catalogCount := 0

	// Error if dont have at least one catalog
	if len(cfg.Catalogs) < 1 {
		return nil, errors.New("unable to continue, no catalogs assigned")
	}

	// Loop through the catalogs and get each one in order
	for _, catalogName := range cfg.Catalogs {

		// Download the catalog
		catalogURL := cfg.URL + "catalogs/" + catalogName + ".yaml"
		slog.Info("Catalog Url", "url", catalogURL)
		yamlFile, err := downloadGet(catalogURL)
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve catalog %s: %w", catalogURL, err)
		}

		// Parse the catalog
		var catalogItems map[string]catalog.Item
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
