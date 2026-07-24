//go:build !production

package main

import (
	"io/fs"
	"os"
)

// The production build embeds frontend/dist. Development builds use the source
// tree so Go tests compile before frontend assets exist.
func bundledAssets() fs.FS {
	return os.DirFS("frontend")
}
