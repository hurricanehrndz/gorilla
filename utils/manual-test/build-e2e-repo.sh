#!/usr/bin/env bash
# Assemble a complete local Gorilla repository for the Chrome end-to-end test.
#
# Output: build/e2e-repo/ (and build/e2e-repo.tar for one-shot transfer), laid
# out exactly as a served repo is, so the VM can point `url:` at it directly
# (file://C:/gorilla-repo/) or an HTTP server can serve it:
#
#   gorilla.exe, gorilla-ui.exe        current build (make build)
#   manifests/                         e2e_manifest + the selfserve fixtures
#   catalogs/                          e2e_catalog (from makecatalogs) + selfserve_catalog
#   packages-info/GoogleChrome.yaml    rendered from the .in template
#   packages/google-chrome/*.msi       Chrome enterprise MSI (cached in build/)
#   packages/scripts/                  selfserve marker installers
#
# Run inside the devenv shell (needs go). Set CHROME_MSI to reuse a downloaded
# MSI instead of fetching it.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

repo=build/e2e-repo
fixtures=utils/manual-test/fixtures
msi_name=googlechromestandaloneenterprise64.msi
msi_url=https://dl.google.com/dl/chrome/install/$msi_name
cache=build/cache
mkdir -p "$cache"

if [[ -n ${CHROME_MSI:-} ]]; then
	cp "$CHROME_MSI" "$cache/$msi_name"
elif [[ ! -s $cache/$msi_name ]]; then
	echo "==> Downloading $msi_url"
	curl -fsSL -o "$cache/$msi_name.part" "$msi_url"
	mv "$cache/$msi_name.part" "$cache/$msi_name"
fi

[[ -x build/gorilla.exe && -x build/gorilla-ui.exe ]] || make build

hash=$(sha256sum "$cache/$msi_name" | cut -d' ' -f1)
# The MSI summary-information Comments field carries "<version> Copyright ...".
version=$(file -b "$cache/$msi_name" | sed -n 's/.*Comments: \([0-9][0-9.]*\).*/\1/p')
[[ -n $version ]] || {
	echo "cannot read the Chrome version from $cache/$msi_name" >&2
	exit 1
}
echo "==> Chrome $version sha256 $hash"

rm -rf "$repo"
mkdir -p "$repo"/{manifests,catalogs,packages-info,packages/google-chrome}
cp build/gorilla.exe build/gorilla-ui.exe "$repo/"
cp "$fixtures"/selfserve/manifests/*.yaml "$fixtures"/e2e/manifests/*.yaml "$repo/manifests/"
cp -R "$fixtures"/selfserve/packages/scripts "$repo/packages/scripts"
cp "$cache/$msi_name" "$repo/packages/google-chrome/$msi_name"
sed -e "s/@VERSION@/$version/g" -e "s/@HASH@/$hash/g" \
	"$fixtures/e2e/packages-info/GoogleChrome.yaml.in" >"$repo/packages-info/GoogleChrome.yaml"

echo "==> makecatalogs"
go run ./cmd/makecatalogs "$repo"
# selfserve_catalog is hand-written (its items are not packages-info files), so
# it is copied in after makecatalogs has replaced catalogs/.
cp "$fixtures"/selfserve/catalogs/*.yaml "$repo/catalogs/"

tar -cf build/e2e-repo.tar -C build e2e-repo
echo "==> $repo ready ($(du -sh build/e2e-repo.tar | cut -f1) tar)"
