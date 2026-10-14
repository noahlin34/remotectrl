// Package config loads and saves remotectrl's TOML configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// FileName is the per-project config file, discovered by walking up from cwd.
const FileName = ".remotectrl.toml"

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
	// Arch optionally forces ARCHS (arm64 / x86_64) for the remote build.
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

// GlobalPath is the fallback config used when no project file is found.
func GlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "remotectrl", "config.toml"), nil
}

// Find returns the nearest project config walking up from cwd, then the global
// one. Returns "" when neither exists.
func Find() (string, error) {
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
	g, err := GlobalPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(g); err == nil {
		return g, nil
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
