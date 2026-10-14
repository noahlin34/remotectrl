// Package remote runs commands on, and pulls files from, the remote Mac over a
// multiplexed ssh connection.
package remote

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Remote struct {
	Host    string
	sshOpts []string
}

// New prepares a Remote. All ssh/rsync traffic shares one ControlMaster
// connection, so Tailscale SSH check-mode / passphrase prompts happen once
// per ControlPersist window rather than once per command.
func New(host string) (*Remote, error) {
	// Unix socket paths are limited to ~104 bytes; os.TempDir() on macOS is too long.
	dir := fmt.Sprintf("/tmp/remotectrl-%d", os.Getuid())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Remote{
		Host: host,
		sshOpts: []string{
			"-o", "ControlMaster=auto",
			"-o", "ControlPath=" + dir + "/%C",
			"-o", "ControlPersist=30m",
			"-o", "ServerAliveInterval=15",
		},
	}, nil
}

// Connect ensures a master connection exists, attached to the terminal so any
// interactive auth (Tailscale check URL, passphrase) is visible.
func (r *Remote) Connect() error {
	check := exec.Command("ssh", append(append([]string{}, r.sshOpts...), "-O", "check", r.Host)...)
	if check.Run() == nil {
		return nil
	}
	start := exec.Command("ssh", append(append([]string{}, r.sshOpts...), "-fN", r.Host)...)
	start.Stdin, start.Stdout, start.Stderr = os.Stdin, os.Stderr, os.Stderr
	if err := start.Run(); err != nil {
		return fmt.Errorf("ssh %s: %w", r.Host, err)
	}
	return nil
}

// command wraps script in a login zsh so PATH (Homebrew etc.) matches an
// interactive terminal on the remote.
func (r *Remote) command(script string) *exec.Cmd {
	args := append(append([]string{}, r.sshOpts...), r.Host, "zsh -lc "+Quote(script))
	return exec.Command("ssh", args...)
}

// Run streams the script's output to the local terminal.
func (r *Remote) Run(script string) error {
	c := r.command(script)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// Output captures stdout; stderr goes to the terminal.
func (r *Remote) Output(script string) ([]byte, error) {
	c := r.command(script)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, os.Stderr
	// A lingering ControlPersist master can keep the pipe open after ssh exits.
	c.WaitDelay = 3 * time.Second
	err := c.Run()
	return out.Bytes(), err
}

// Pull mirrors the remote directory remoteAbs into localDir (delta transfer,
// deleting files that no longer exist remotely). Only the changed parts of a
// rebuilt .app cross the network after the first sync.
func (r *Remote) Pull(remoteAbs, localDir string) error {
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return err
	}
	// Remote paths are word-split by the remote shell; escape spaces.
	src := r.Host + ":" + strings.ReplaceAll(strings.TrimRight(remoteAbs, "/"), " ", `\ `) + "/"
	ssh := "ssh " + strings.Join(r.sshOpts, " ")
	c := exec.Command("rsync", "-a", "--delete", "-e", ssh, src, filepath.Clean(localDir)+"/")
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

// Quote single-quotes s for a POSIX shell.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DirExpr turns a user-supplied remote dir into a shell expression, expanding a
// leading ~ to $HOME while quoting the rest.
func DirExpr(dir string) string {
	switch {
	case dir == "~":
		return `"$HOME"`
	case strings.HasPrefix(dir, "~/"):
		return `"$HOME"/` + Quote(dir[2:])
	}
	return Quote(dir)
}
