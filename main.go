// remotectrl builds an iOS app on a remote (headless) Mac and runs it in the
// local iOS Simulator.
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/noahlin34/remotectrl/internal/config"
	"github.com/noahlin34/remotectrl/internal/remote"
	"github.com/noahlin34/remotectrl/internal/sim"
	"github.com/noahlin34/remotectrl/internal/xcode"
)

const usage = `remotectrl - build on a remote Mac, run in the local iOS Simulator

Usage:
  remotectrl init [flags]     detect workspace/scheme on the remote and write ` + config.FileName + `
  remotectrl run [flags]      build remotely, sync the .app here, install + launch in the Simulator
  remotectrl build [flags]    build remotely only
  remotectrl sync [flags]     sync the last remote build here and install + launch (no build)
  remotectrl doctor [flags]   check local and remote prerequisites

Common flags (override the config file):
  --host --dir --workspace --project --scheme --target --configuration --arch --device

Config is read from the nearest ` + config.FileName + ` above the current directory,
falling back to ~/.config/remotectrl/config.toml.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "init":
		err = cmdInit(args)
	case "run":
		err = cmdRun(args, true)
	case "sync":
		err = cmdRun(args, false)
	case "build":
		err = cmdBuild(args)
	case "doctor":
		err = cmdDoctor(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "remotectrl:", err)
		os.Exit(1)
	}
}

// overrides are the flags every command shares; non-empty values beat the config file.
type overrides struct{ config.Config }

func (o *overrides) bind(fs *flag.FlagSet) {
	fs.StringVar(&o.Host, "host", "", "ssh host of the remote Mac (tailnet name, alias, user@host)")
	fs.StringVar(&o.Dir, "dir", "", "project directory on the remote Mac")
	fs.StringVar(&o.Workspace, "workspace", "", ".xcworkspace relative to dir")
	fs.StringVar(&o.Project, "project", "", ".xcodeproj relative to dir")
	fs.StringVar(&o.Scheme, "scheme", "", "scheme to build")
	fs.StringVar(&o.Target, "target", "", "app target when the scheme builds several")
	fs.StringVar(&o.Configuration, "configuration", "", "build configuration (default Debug)")
	fs.StringVar(&o.Arch, "arch", "", "force ARCHS for the remote build (arm64|x86_64)")
	fs.StringVar(&o.Simulator, "device", "", `simulator: "booted" (default), name, or UDID`)
}

func (o *overrides) merge(base config.Config) config.Config {
	pick := func(flagVal, cfgVal string) string {
		if flagVal != "" {
			return flagVal
		}
		return cfgVal
	}
	c := base
	c.Host = pick(o.Host, c.Host)
	c.Dir = pick(o.Dir, c.Dir)
	c.Scheme = pick(o.Scheme, c.Scheme)
	c.Target = pick(o.Target, c.Target)
	c.Configuration = pick(o.Configuration, c.Configuration)
	c.Arch = pick(o.Arch, c.Arch)
	c.Simulator = pick(o.Simulator, c.Simulator)
	// workspace and project are mutually exclusive: a flag for one replaces both.
	if o.Workspace != "" || o.Project != "" {
		c.Workspace, c.Project = o.Workspace, o.Project
	}
	return c
}

func loadConfig(o *overrides) (config.Config, error) {
	base, _, err := config.Load()
	if err != nil {
		return base, err
	}
	c := o.merge(base)
	c.ApplyDefaults()
	return c, c.Validate()
}

func step(format string, a ...any) { fmt.Printf("\033[1m==> "+format+"\033[0m\n", a...) }

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// localAppDir is the persistent per-project mirror that rsync diffs against, so
// rebuilds only transfer changed files.
func localAppDir(c config.Config) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(c.Dir + "\x00" + c.Scheme))
	key := unsafeChars.ReplaceAllString(c.Host, "_") + "-" + hex.EncodeToString(sum[:4])
	return filepath.Join(cache, "remotectrl", key), nil
}

func connect(c config.Config) (*remote.Remote, error) {
	r, err := remote.New(c.Host)
	if err != nil {
		return nil, err
	}
	return r, r.Connect()
}

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	var o overrides
	o.bind(fs)
	raw := fs.Bool("raw", false, "don't pipe through xcbeautify")
	fs.Parse(args)
	c, err := loadConfig(&o)
	if err != nil {
		return err
	}
	r, err := connect(c)
	if err != nil {
		return err
	}
	return build(r, c, *raw)
}

func build(r *remote.Remote, c config.Config, raw bool) error {
	step("Building %s (%s) on %s", c.Scheme, c.Configuration, c.Host)
	t := time.Now()
	if err := xcode.Build(r, c, raw); err != nil {
		return fmt.Errorf("remote build failed: %w", err)
	}
	fmt.Printf("built in %s\n", time.Since(t).Round(time.Millisecond))
	return nil
}

// cmdRun implements both `run` (withBuild) and `sync`.
func cmdRun(args []string, withBuild bool) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var o overrides
	o.bind(fs)
	raw := fs.Bool("raw", false, "don't pipe the build through xcbeautify")
	console := fs.Bool("console", false, "stay attached and stream the app's stdout/stderr")
	fs.Parse(args)
	c, err := loadConfig(&o)
	if err != nil {
		return err
	}
	r, err := connect(c)
	if err != nil {
		return err
	}
	if withBuild {
		if err := build(r, c, *raw); err != nil {
			return err
		}
	}

	app, err := xcode.Locate(r, c)
	if err != nil {
		return err
	}
	dir, err := localAppDir(c)
	if err != nil {
		return err
	}
	local := filepath.Join(dir, app.Name)

	step("Syncing %s", app.Name)
	t := time.Now()
	if err := r.Pull(app.RemotePath, local); err != nil {
		return fmt.Errorf("rsync: %w", err)
	}
	fmt.Printf("synced in %s\n", time.Since(t).Round(time.Millisecond))

	bundleID, err := sim.BundleID(local)
	if err != nil {
		return err
	}
	dev, err := sim.Resolve(c.Simulator)
	if err != nil {
		return err
	}
	step("Installing %s on %s", bundleID, dev.Name)
	if err := sim.EnsureBooted(dev); err != nil {
		return err
	}
	if err := sim.Install(dev, local); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	step("Launching")
	return sim.Launch(dev, bundleID, *console)
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	var o overrides
	o.bind(fs)
	global := fs.Bool("global", false, "write ~/.config/remotectrl/config.toml instead of ./"+config.FileName)
	force := fs.Bool("force", false, "overwrite an existing config")
	fs.Parse(args)

	c := o.merge(config.Config{})
	if c.Host == "" || c.Dir == "" {
		return fmt.Errorf("init needs --host and --dir")
	}
	path := config.FileName
	if *global {
		g, err := config.GlobalPath()
		if err != nil {
			return err
		}
		path = g
	}
	if _, err := os.Stat(path); err == nil && !*force {
		return fmt.Errorf("%s exists (use --force)", path)
	}

	r, err := connect(c)
	if err != nil {
		return err
	}
	ws, proj, schemes, err := xcode.Discover(r, c.Dir)
	if err != nil {
		return err
	}
	if c.Workspace == "" && c.Project == "" {
		c.Workspace, c.Project = ws, proj
	}
	if c.Scheme == "" {
		if len(schemes) != 1 {
			return fmt.Errorf("pick a scheme with --scheme; available: %s", strings.Join(schemes, ", "))
		}
		c.Scheme = schemes[0]
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if err := config.Save(path, c); err != nil {
		return err
	}
	fmt.Printf("wrote %s (scheme %q)\n", path, c.Scheme)
	return nil
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	var o overrides
	o.bind(fs)
	fs.Parse(args)

	failed := false
	check := func(name string, err error, detail string) {
		if err != nil {
			failed = true
			fmt.Printf("  ✗ %s: %v\n", name, err)
			return
		}
		fmt.Printf("  ✓ %s %s\n", name, detail)
	}
	look := func(bin string) { _, err := exec.LookPath(bin); check("local "+bin, err, "") }

	fmt.Println("local")
	look("ssh")
	look("rsync")
	look("plutil")
	_, err := sim.Resolve("booted")
	check("booted simulator", err, "")

	base, path, err := config.Load()
	if err != nil {
		return err
	}
	c := o.merge(base)
	c.ApplyDefaults()
	fmt.Printf("config: %s\n", orNone(path))
	if err := c.Validate(); err != nil {
		check("config", err, "")
		return fmt.Errorf("doctor found problems")
	}

	fmt.Printf("remote (%s)\n", c.Host)
	r, err := connect(c)
	if err != nil {
		check("ssh", err, "")
		return fmt.Errorf("doctor found problems")
	}
	check("ssh", nil, "")
	probe := func(name, script string) {
		out, err := r.Output(script)
		check(name, err, strings.TrimSpace(string(out)))
	}
	probe("project dir", "cd "+remote.DirExpr(c.Dir)+" && pwd")
	probe("xcodebuild", "xcodebuild -version | tr '\\n' ' '")
	probe("rsync", "command -v rsync")
	if out, _ := r.Output("command -v xcbeautify"); len(out) > 0 {
		fmt.Printf("  ✓ xcbeautify %s", out)
	} else {
		fmt.Println("  - xcbeautify not installed (optional; prettier build logs)")
	}
	_, err = xcode.Locate(r, c)
	check("scheme "+c.Scheme, err, "builds an iOS app")

	if failed {
		return fmt.Errorf("doctor found problems")
	}
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
