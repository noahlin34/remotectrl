package xcode

import (
	"strings"

	"github.com/noahlin34/remotectrl/internal/remote"
)

// A directory is a Tuist project when it carries a Tuist manifest. The Xcode
// workspace/project is then a generated (usually gitignored) artifact, so it
// must be regenerated before building or it can be missing or stale (e.g. after
// adding a source file, since Tuist globs sources at generate time).
const tuistDetect = `[ -f Project.swift ] || [ -f Workspace.swift ] || [ -f Tuist.swift ]`

// tuistResolve makes a `tuist` command available in the remote shell. Tool
// versions managed by mise take precedence (non-interactive ssh shells don't
// activate mise, so go through `mise exec`); otherwise tuist must be on PATH.
const tuistResolve = `export PATH="$HOME/.local/bin:$PATH"
if command -v mise >/dev/null 2>&1 && { [ -f mise.toml ] || [ -f .mise.toml ] || [ -f .tool-versions ]; }; then
  tuist() { command mise exec -- tuist "$@"; }
elif ! command -v tuist >/dev/null 2>&1; then
  echo "remotectrl: Tuist project but tuist not found (no mise config in the project dir, none on PATH)" >&2
  exit 127
fi
`

// Options controls the remote build.
type Options struct {
	// Raw disables the xcbeautify pipe.
	Raw bool
	// NoGenerate skips Tuist install/generate (manifests unchanged since last run).
	NoGenerate bool
	// TuistInstall forces `tuist install` even when Tuist/.build already exists.
	TuistInstall bool
}

// tuistPrepare is a shell fragment, run inside the project dir, that installs
// dependencies (only when missing, or forced) and regenerates the project. It
// is a no-op for non-Tuist directories.
func tuistPrepare(o Options) string {
	force := "false"
	if o.TuistInstall {
		force = "true"
	}
	var b strings.Builder
	b.WriteString("if " + tuistDetect + "; then\n")
	b.WriteString(tuistResolve)
	b.WriteString("  if [ -f Tuist/Package.swift ] && { [ ! -d Tuist/.build ] || " + force + "; }; then\n")
	b.WriteString("    echo '==> tuist install'; tuist install || exit $?\n  fi\n")
	b.WriteString("  echo '==> tuist generate'; tuist generate --no-open || exit $?\n")
	b.WriteString("fi\n")
	return b.String()
}

// Prepare runs only the Tuist step (used by `init`, where a fresh clone has no
// generated workspace to discover yet).
func Prepare(r *remote.Remote, dir string, o Options) error {
	return r.Run("cd " + remote.DirExpr(dir) + " || exit 1\n" + tuistPrepare(o))
}

// TuistVersion reports whether dir is a Tuist project and, if so, the version
// the remote would use. A non-nil error means a Tuist project whose tuist
// can't be run.
func TuistVersion(r *remote.Remote, dir string) (isTuist bool, version string, err error) {
	out, err := r.Output("cd " + remote.DirExpr(dir) + " || exit 1\n" +
		"if " + tuistDetect + "; then\n" + tuistResolve + "echo \"$(tuist version)\"\nelse echo -; fi")
	v := strings.TrimSpace(string(out))
	if err != nil {
		return true, "", err
	}
	if v == "-" {
		return false, "", nil
	}
	if i := strings.LastIndexByte(v, '\n'); i >= 0 {
		v = v[i+1:]
	}
	return true, v, nil
}
