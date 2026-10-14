// Package xcode drives xcodebuild on the remote Mac. It deliberately never
// passes -derivedDataPath: builds land in Xcode's default DerivedData so they
// share caches with Xcode itself, and the product is located afterwards via
// -showBuildSettings.
package xcode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/noahlin34/remotectrl/internal/config"
	"github.com/noahlin34/remotectrl/internal/remote"
)

const appProductType = "com.apple.product-type.application"

type App struct {
	Target string
	// RemotePath is the absolute path of the built .app on the remote Mac.
	RemotePath string
	// Name is the .app bundle directory name, e.g. "MyApp.app".
	Name string
}

// buildArgs are the xcodebuild flags shared by build and -showBuildSettings.
func buildArgs(c config.Config) []string {
	var a []string
	switch {
	case c.Workspace != "":
		a = append(a, "-workspace", c.Workspace)
	case c.Project != "":
		a = append(a, "-project", c.Project)
	}
	a = append(a,
		"-scheme", c.Scheme,
		"-configuration", c.Configuration,
		"-sdk", "iphonesimulator",
		"-destination", "generic/platform=iOS Simulator",
	)
	if c.Arch != "" {
		a = append(a, "ARCHS="+c.Arch, "ONLY_ACTIVE_ARCH=NO")
	}
	return a
}

func quoteAll(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = remote.Quote(a)
	}
	return strings.Join(q, " ")
}

// Build runs xcodebuild on the remote, piping through xcbeautify when it is
// installed there (unless raw).
func Build(r *remote.Remote, c config.Config, raw bool) error {
	cmd := "xcodebuild " + quoteAll(buildArgs(c)) + " build"
	script := "set -o pipefail\ncd " + remote.DirExpr(c.Dir) + " || exit 1\n"
	if raw {
		script += cmd
	} else {
		script += "if command -v xcbeautify >/dev/null 2>&1; then " + cmd + " 2>&1 | xcbeautify; else " + cmd + "; fi"
	}
	return r.Run(script)
}

type buildSettings struct {
	Target        string            `json:"target"`
	BuildSettings map[string]string `json:"buildSettings"`
}

// Locate asks Xcode where the built app lives, wherever DerivedData is.
func Locate(r *remote.Remote, c config.Config) (App, error) {
	script := "cd " + remote.DirExpr(c.Dir) + " || exit 1\n" +
		"xcodebuild " + quoteAll(buildArgs(c)) + " -showBuildSettings -json 2>/dev/null"
	out, err := r.Output(script)
	if err != nil {
		return App{}, fmt.Errorf("xcodebuild -showBuildSettings: %w", err)
	}
	// Tolerate login-shell noise before the JSON array.
	if i := bytes.IndexByte(out, '['); i >= 0 {
		out = out[i:]
	}
	var targets []buildSettings
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&targets); err != nil {
		return App{}, fmt.Errorf("parsing build settings: %w", err)
	}

	var apps []App
	for _, t := range targets {
		s := t.BuildSettings
		if s["PRODUCT_TYPE"] != appProductType || s["WRAPPER_EXTENSION"] != "app" {
			continue
		}
		if c.Target != "" && t.Target != c.Target {
			continue
		}
		apps = append(apps, App{
			Target:     t.Target,
			RemotePath: s["TARGET_BUILD_DIR"] + "/" + s["FULL_PRODUCT_NAME"],
			Name:       s["FULL_PRODUCT_NAME"],
		})
	}
	switch len(apps) {
	case 0:
		return App{}, fmt.Errorf("scheme %q builds no iOS app target", c.Scheme)
	case 1:
		return apps[0], nil
	}
	names := make([]string, len(apps))
	for i, a := range apps {
		names[i] = a.Target
	}
	return App{}, fmt.Errorf("scheme %q builds several apps %v; set `target` in config", c.Scheme, names)
}

// Discover finds the workspace/project in dir and the available schemes, for `init`.
func Discover(r *remote.Remote, dir string) (workspace, project string, schemes []string, err error) {
	out, err := r.Output("cd " + remote.DirExpr(dir) + " || exit 1\n" +
		`find . -maxdepth 1 \( -name '*.xcworkspace' -o -name '*.xcodeproj' \) | sort`)
	if err != nil {
		return "", "", nil, fmt.Errorf("listing %s: %w", dir, err)
	}
	var wss, projs []string
	for _, l := range strings.Fields(string(out)) {
		l = strings.TrimPrefix(l, "./")
		if strings.HasSuffix(l, ".xcworkspace") {
			wss = append(wss, l)
		} else if strings.HasSuffix(l, ".xcodeproj") {
			projs = append(projs, l)
		}
	}
	var flag string
	switch {
	case len(wss) > 0:
		workspace, flag = wss[0], "-workspace "+remote.Quote(wss[0])
	case len(projs) > 0:
		project, flag = projs[0], "-project "+remote.Quote(projs[0])
	default:
		return "", "", nil, fmt.Errorf("no .xcworkspace or .xcodeproj directly in %s", dir)
	}

	out, err = r.Output("cd " + remote.DirExpr(dir) + " || exit 1\nxcodebuild -list -json " + flag + " 2>/dev/null")
	if err != nil {
		return "", "", nil, fmt.Errorf("xcodebuild -list: %w", err)
	}
	if i := bytes.IndexByte(out, '{'); i >= 0 {
		out = out[i:]
	}
	var list struct {
		Workspace struct{ Schemes []string } `json:"workspace"`
		Project   struct{ Schemes []string } `json:"project"`
	}
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&list); err != nil {
		return "", "", nil, fmt.Errorf("parsing xcodebuild -list: %w", err)
	}
	schemes = append(list.Workspace.Schemes, list.Project.Schemes...)
	return workspace, project, schemes, nil
}
