package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// serviceInstallHint tells the user how to install the login service later,
// printed whenever the post-init offer does not install it.
const serviceInstallHint = "To run continuously in the background, install the login service:\n  synckeeper service install"

// offerServiceInstall carries out the daemon-first onboarding offer at the end
// of `init` (spec §1). It never fails init: a declined, skipped, or failed
// offer only prints guidance. Precedence: skip (--no-service) wins over force
// (--service); an interactive terminal is prompted; otherwise a hint is printed
// (never install a background service without consent in a non-interactive run).
//
// install performs the actual install (service.Install) and returns its
// human-readable summary; it is injected so this decision logic is testable
// without touching the OS service manager.
func offerServiceInstall(in io.Reader, out io.Writer, force, skip, interactive bool, install func() (string, error)) {
	switch {
	case skip:
		fmt.Fprintln(out, serviceInstallHint)
		return
	case force:
		// install below
	case interactive:
		if !promptYesNo(in, out, "Start Synckeeper automatically at login?", true) {
			fmt.Fprintln(out, serviceInstallHint)
			return
		}
	default:
		fmt.Fprintln(out, serviceInstallHint)
		return
	}
	msg, err := install()
	if err != nil {
		fmt.Fprintf(out, "could not install the login service: %v\n%s\n", err, serviceInstallHint)
		return
	}
	fmt.Fprintln(out, msg)
}

// promptYesNo asks a yes/no question and reads one line. def is the answer for
// an empty line; an EOF, read error, or unrecognized reply also falls back to
// def, so a piped or closed stdin never blocks or surprises.
func promptYesNo(in io.Reader, out io.Writer, question string, def bool) bool {
	suffix := " [Y/n]: "
	if !def {
		suffix = " [y/N]: "
	}
	fmt.Fprint(out, question+suffix)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default: // empty line or anything else
		return def
	}
}

// isTerminal reports whether f is a character device (a terminal), used to
// decide whether `init` may prompt.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
