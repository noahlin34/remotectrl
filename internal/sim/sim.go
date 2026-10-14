// Package sim wraps xcrun simctl on the local Mac.
package sim

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type Device struct {
	UDID    string `json:"udid"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Runtime string `json:"-"`
}

func (d Device) Booted() bool { return d.State == "Booted" }

// List returns all available iOS simulators, newest runtime first, then by name.
func List() ([]Device, error) {
	out, err := exec.Command("xcrun", "simctl", "list", "devices", "available", "-j").Output()
	if err != nil {
		return nil, fmt.Errorf("simctl list: %w", err)
	}
	var resp struct {
		Devices map[string][]Device `json:"devices"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	var all []Device
	for rt, ds := range resp.Devices {
		if !strings.Contains(rt, "SimRuntime.iOS-") {
			continue
		}
		for _, d := range ds {
			d.Runtime = rt
			all = append(all, d)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		vi, vj := runtimeVersion(all[i].Runtime), runtimeVersion(all[j].Runtime)
		if !equal(vi, vj) {
			return newer(vi, vj)
		}
		return all[i].Name < all[j].Name
	})
	return all, nil
}

func equal(a, b []int) bool { return !newer(a, b) && !newer(b, a) }

// IOSVersion is the runtime's iOS version, e.g. "26.2".
func (d Device) IOSVersion() string {
	v := runtimeVersion(d.Runtime)
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ".")
}

// runtimeVersion parses "...SimRuntime.iOS-26-2" into [26 2].
func runtimeVersion(rt string) []int {
	i := strings.LastIndex(rt, "iOS-")
	if i < 0 {
		return nil
	}
	var v []int
	for _, p := range strings.Split(rt[i+4:], "-") {
		n, _ := strconv.Atoi(p)
		v = append(v, n)
	}
	return v
}

func newer(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return len(a) > len(b)
}

// Resolve picks a device from spec: "booted", a UDID, or a name (newest iOS
// runtime wins; an already-booted match is preferred).
func Resolve(spec string) (Device, error) {
	devs, err := List()
	if err != nil {
		return Device{}, err
	}
	var best *Device
	for i := range devs {
		d := &devs[i]
		switch {
		case spec == "booted":
			if !d.Booted() {
				continue
			}
		case d.UDID != spec && !strings.EqualFold(d.Name, spec):
			continue
		}
		if best == nil ||
			(d.Booted() && !best.Booted()) ||
			(d.Booted() == best.Booted() && newer(runtimeVersion(d.Runtime), runtimeVersion(best.Runtime))) {
			best = d
		}
	}
	if best == nil {
		if spec == "booted" {
			return Device{}, fmt.Errorf("no booted simulator; boot one or set `simulator` to a device name (e.g. \"iPhone 17\")")
		}
		return Device{}, fmt.Errorf("no available iOS simulator matches %q", spec)
	}
	return *best, nil
}

// EnsureBooted boots d if needed and brings Simulator.app to the front.
func EnsureBooted(d Device) error {
	if !d.Booted() {
		if err := run("xcrun", "simctl", "bootstatus", d.UDID, "-b"); err != nil {
			return fmt.Errorf("booting %s: %w", d.Name, err)
		}
	}
	return exec.Command("open", "-a", "Simulator").Run()
}

func Install(d Device, appPath string) error {
	return run("xcrun", "simctl", "install", d.UDID, appPath)
}

// Launch (re)starts the app. With console, it stays attached and streams the
// app's stdout/stderr until interrupted.
func Launch(d Device, bundleID string, console bool) error {
	args := []string{"simctl", "launch", "--terminate-running-process"}
	if console {
		args = append(args, "--console")
	}
	return run("xcrun", append(args, d.UDID, bundleID)...)
}

// BundleID reads CFBundleIdentifier from the local .app's Info.plist.
func BundleID(appPath string) (string, error) {
	out, err := exec.Command("plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", appPath+"/Info.plist").Output()
	if err != nil {
		return "", fmt.Errorf("reading bundle id from %s: %w", appPath, err)
	}
	return string(bytes.TrimSpace(out)), nil
}

func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}
