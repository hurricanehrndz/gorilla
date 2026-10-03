// Command makecatalogs compiles a Gorilla repository's packages-info files
// into its catalogs, like Munki's makecatalogs. It runs on any platform and
// reads no agent configuration.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/1dustindavis/gorilla/pkg/admin"
	"github.com/1dustindavis/gorilla/pkg/version"
)

const usage = `Usage: makecatalogs [options] <repo_path>

Compiles <repo_path>/packages-info/*.yaml into <repo_path>/catalogs/.

Options:
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes makecatalogs and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("makecatalogs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	check := flags.Bool("check", false, "validate the repo and report what would be written, without writing; exit nonzero on any problem")
	showVersion := flags.Bool("version", false, "print the version and exit")
	flags.Usage = func() {
		_, _ = fmt.Fprint(stderr, usage)
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		_, _ = fmt.Fprintf(stdout, "makecatalogs %s\n", version.Version().Version)
		return 0
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	repoPath := flags.Arg(0)

	set, err := admin.CollectCatalogs(repoPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "makecatalogs:", err)
		return 1
	}

	verb := "Wrote"
	if *check {
		verb = "Would write"
	} else {
		for _, problem := range set.Problems {
			_, _ = fmt.Fprintln(stderr, "warning:", problem)
		}
		if err := admin.WriteCatalogs(repoPath, set); err != nil {
			_, _ = fmt.Fprintln(stderr, "makecatalogs:", err)
			return 1
		}
	}
	for _, name := range set.Names() {
		_, _ = fmt.Fprintf(stdout, "%s catalogs/%s.yaml (%d items)\n", verb, name, len(set.Catalogs[name]))
	}

	if *check && len(set.Problems) > 0 {
		for _, problem := range set.Problems {
			_, _ = fmt.Fprintln(stderr, "error:", problem)
		}
		_, _ = fmt.Fprintf(stderr, "makecatalogs: check failed with %d problem(s)\n", len(set.Problems))
		return 1
	}
	return 0
}
