package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestCLISurfaceMatchesManifest is a golden guard on the CLI surface: the set
// of commands and each command's flags must match the manifest below. Adding,
// renaming, or removing a command or flag fails this test until the manifest —
// and the matching MANUAL.md §3 / spec §15 rows — are updated in the same
// commit. It exists because a doc claim about the CLI surface is a claim about
// this tree; the type system, not review, keeps them in step (CLAUDE.md,
// "Doc claims are claims about code"; decisions.md 2026-07-24).
//
// When this fails: update the manifest AND the MANUAL/spec rows together, or the
// change is not done.
func TestCLISurfaceMatchesManifest(t *testing.T) {
	// command name -> its non-inherited flags (long names), as documented.
	// This MUST include cobra's auto-added built-ins (help, completion): they
	// are invocable and belong in MANUAL §3. They were once missing from the
	// manual precisely because an earlier version of this test enumerated the
	// tree before Execute(), when cobra hasn't materialized them yet — so we
	// materialize them explicitly below (decisions.md 2026-07-25).
	want := map[string][]string{
		"init":       {"adopt", "force", "no-service", "service"},
		"login":      {},
		"status":     {"json", "watch"},
		"activity":   {"number"},
		"config":     {},
		"account":    {},
		"sync":       {"confirm-deletes", "dry-run"},
		"pause":      {},
		"resume":     {},
		"reload":     {},
		"doctor":     {"repair"},
		"watch":      {},
		"service":    {},
		"help":       {},
		"completion": {},
	}

	root := newRootCmd()
	// Force cobra to add the built-ins it otherwise adds only during Execute().
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	root.InitDefaultVersionFlag()

	// Global flags documented in MANUAL §3.
	if f := root.PersistentFlags().Lookup("verbose"); f == nil || f.Shorthand != "v" {
		t.Errorf("root persistent flag -v/--verbose changed or missing; update MANUAL §3 (global flags) and this test")
	}
	if root.Flags().Lookup("version") == nil {
		t.Errorf("root --version flag missing; update MANUAL §3 (global flags) and this test")
	}

	got := map[string][]string{}
	for _, c := range root.Commands() {
		name := strings.Fields(c.Use)[0]
		got[name] = localFlagNames(c)
	}

	// Same set of commands?
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("command %q is documented (manifest/MANUAL §3) but missing from the CLI", name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("command %q exists in the CLI but is not in the manifest — add it here and to MANUAL §3 / spec §15", name)
		}
	}

	// Same flags per command?
	for name, wantFlags := range want {
		gotFlags, ok := got[name]
		if !ok {
			continue // already reported above
		}
		if !equalStrings(wantFlags, gotFlags) {
			t.Errorf("command %q flags = %v, manifest/MANUAL expects %v — reconcile the CLI, this manifest, and MANUAL §3 / spec §15",
				name, gotFlags, wantFlags)
		}
	}
}

// localFlagNames returns a command's own flag long-names, sorted, excluding the
// auto-added "help" and any inherited persistent flag ("verbose").
func localFlagNames(c *cobra.Command) []string {
	names := []string{}
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" || f.Name == "verbose" {
			return
		}
		names = append(names, f.Name)
	})
	sort.Strings(names)
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
