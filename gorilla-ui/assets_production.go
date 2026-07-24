//go:build production

package main

import (
	"embed"
	"io/fs"
)

//go:embed frontend/dist
var embeddedAssets embed.FS

func bundledAssets() fs.FS {
	assets, err := fs.Sub(embeddedAssets, "frontend/dist")
	if err != nil {
		panic(err)
	}
	return assets
}
