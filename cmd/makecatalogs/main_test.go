package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/1dustindavis/gorilla/pkg/catalog"
	"go.yaml.in/yaml/v4"
)

// writeRepo creates a scratch repo with the given packages-info files.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	for name, body := range files {
		path := filepath.Join(repo, "packages-info", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// snapshot returns every file under dir keyed by relative path.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files[rel] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

const chromeInfo = `
item_name: Chrome
display_name: Google Chrome
catalog: production
version: 1.2.3.4
installer:
  type: nupkg
  location: packages/chrome/chrome.nupkg
  hash: abc
`

// The research's headline defect: `gorilla -build` exited before doing any
// work unless an agent config file with a manifest and url existed. With no
// config anywhere, makecatalogs must still build the catalog.
func TestBuildsCatalogWithoutAgentConfig(t *testing.T) {
	t.Setenv("ProgramData", t.TempDir()) // empty: no gorilla/config.yaml
	t.Chdir(t.TempDir())                 // and none relative to the working directory
	repo := writeRepo(t, map[string]string{"chrome.yaml": chromeInfo})

	var stdout, stderr bytes.Buffer
	if code := run([]string{repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr.String())
	}

	body, err := os.ReadFile(filepath.Join(repo, "catalogs", "production.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]catalog.Item
	if err := yaml.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	chrome, ok := got["Chrome"]
	if len(got) != 1 || !ok {
		t.Fatalf("want only Chrome, got %v", got)
	}
	wantInstaller := catalog.InstallerItem{Type: "nupkg", Location: "packages/chrome/chrome.nupkg", Hash: "abc"}
	if chrome.DisplayName != "Google Chrome" || chrome.Version != "1.2.3.4" ||
		chrome.Installer.Type != wantInstaller.Type || chrome.Installer.Location != wantInstaller.Location || chrome.Installer.Hash != wantInstaller.Hash {
		t.Fatalf("unexpected Chrome item: %#v", chrome)
	}
}

// --check is a PR gate: it must never write, and must fail on anything a
// normal build would only warn about.
func TestCheckWritesNothing(t *testing.T) {
	tests := map[string]struct {
		files    map[string]string
		wantCode int
	}{
		"good repo": {map[string]string{"chrome.yaml": chromeInfo}, 0},
		"missing catalog": {map[string]string{
			"chrome.yaml": chromeInfo,
			"orphan.yaml": "item_name: Orphan\n",
		}, 1},
		"duplicate item": {map[string]string{
			"chrome.yaml":   chromeInfo,
			"chrome-2.yaml": chromeInfo,
		}, 1},
		"invalid yaml": {map[string]string{"bad.yaml": ":\n- not valid yaml"}, 1},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			repo := writeRepo(t, tc.files)
			stale := filepath.Join(repo, "catalogs", "stale.yaml")
			if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stale, []byte("Old: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, repo)

			var stdout, stderr bytes.Buffer
			if code := run([]string{"--check", repo}, &stdout, &stderr); code != tc.wantCode {
				t.Fatalf("exit %d, want %d; stderr: %s", code, tc.wantCode, stderr.String())
			}
			if after := snapshot(t, repo); !reflect.DeepEqual(before, after) {
				t.Fatalf("--check modified the repo")
			}
		})
	}
}

func TestBuildWarnsButSucceedsOnProblems(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"chrome.yaml": chromeInfo,
		"orphan.yaml": "item_name: Orphan\n",
	})
	var stdout, stderr bytes.Buffer
	if code := run([]string{repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning:") {
		t.Fatalf("expected a warning, got: %s", stderr.String())
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"a", "b"}, {"--force", "repo"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code == 0 {
			t.Errorf("args %q: expected nonzero exit", args)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{t.TempDir()}, &stdout, &stderr); code != 1 {
		t.Errorf("repo without packages-info: exit %d, want 1", code)
	}
}

// makecatalogs ships for linux/darwin/windows from a pure-Go build. If it ever
// links the agent's config/download stack or Windows syscalls again, the
// cross-platform split has regressed.
func TestDependencyLeaf(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go toolchain not on PATH: %v", err)
	}
	forbidden := []string{
		"github.com/1dustindavis/gorilla/pkg/config",
		"github.com/1dustindavis/gorilla/pkg/download",
		"golang.org/x/sys/windows",
	}
	for _, goos := range []string{"linux", "windows", "darwin"} {
		cmd := exec.Command(goBin, "list", "-deps", ".")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=amd64", "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("GOOS=%s go list: %v", goos, err)
		}
		for _, dep := range strings.Fields(string(out)) {
			for _, bad := range forbidden {
				if dep == bad || strings.HasPrefix(dep, bad+"/") {
					t.Errorf("GOOS=%s: makecatalogs depends on %s", goos, dep)
				}
			}
		}
	}
}
