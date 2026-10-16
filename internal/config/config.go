// Package config loads and saves remotectrl's TOML configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

// FileName is the project-local config, used only when no global config exists.
const FileName = ".remotectrl.toml"

// GlobalName is the user-level config under ~/.config/remotectrl. It takes
// precedence over any project file.
const GlobalName = "remotectrl.toml"

type Config struct {
	// Host is anything ssh accepts: a tailnet name, an ssh alias, user@host.
	Host string `toml:"host"`
	// Dir is the project directory on the remote Mac ("~/" is expanded remotely).
	Dir       string `toml:"dir"`
	Workspace string `toml:"workspace,omitempty"`
	Project   string `toml:"project,omitempty"`
	Scheme    string `toml:"scheme"`
	// Target picks the app target when a scheme builds more than one app.
	Target        string `toml:"target,omitempty"`
	Configuration string `toml:"configuration,omitempty"`
	// Arch is the ARCHS value for the remote build. It defaults to this Mac's
	// architecture (arm64 on Apple silicon), since the simulator runs here;
	// without it a generic simulator build compiles every arch.
	Arch string `toml:"arch,omitempty"`
	// Simulator is "booted", a device name, or a UDID.
	Simulator string `toml:"simulator,omitempty"`
}

func (c *Config) ApplyDefaults() {
	if c.Configuration == "" {
		c.Configuration = "Debug"
	}
	if c.Simulator == "" {
		c.Simulator = "booted"
	}
	if c.Arch == "" {
		c.Arch = localArch()
	}
}

func localArch() string {
	if runtime.GOARCH == "amd64" {
		return "x86_64"
	}
	return runtime.GOARCH
}

func (c Config) Validate() error {
	var missing []string
	if c.Host == "" {
		missing = append(missing, "host")
	}
	if c.Dir == "" {
		missing = append(missing, "dir")
	}
	if c.Scheme == "" {
		missing = append(missing, "scheme")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %v (run `remotectrl init` or pass flags)", missing)
	}
	if c.Workspace != "" && c.Project != "" {
		return errors.New("set only one of workspace / project")
	}
	return nil
}

// GlobalPath is the user-level config, preferred over any project file.
func GlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "remotectrl", GlobalName), nil
}

// Find returns the global config when it exists, otherwise the nearest project
// config walking up from cwd. Returns "" when neither exists.
func Find() (string, error) {
	g, err := GlobalPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(g); err == nil {
		return g, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, FileName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", nil
}

// Load reads the discovered config. A missing config yields a zero Config.
func Load() (Config, string, error) {
	var c Config
	p, err := Find()
	if err != nil || p == "" {
		return c, "", err
	}
	if _, err := toml.DecodeFile(p, &c); err != nil {
		return c, p, fmt.Errorf("%s: %w", p, err)
	}
	return c, p, nil
}

func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
