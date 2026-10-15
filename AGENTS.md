# AGENTS.md

Guidance for AI agents and contributors working on `remotectrl`.

## What this is

`remotectrl` is a Go CLI that builds an iOS app on a remote (headless) Mac over SSH, then pulls the resulting `.app` to the local Mac and installs and launches it in the local iOS Simulator. The local Mac is where the Simulator runs. The remote Mac is where Xcode and Tuist run.

Module: `github.com/noahlin34/remotectrl`. Go 1.25. One external dependency: `github.com/BurntSushi/toml`.

## Layout

```
main.go                       CLI entrypoint, subcommands, flag binding, orchestration
ui.go                         Terminal helpers: step(), bold/dim/green/yellow, pad(), color gating
internal/config/config.go     .remotectrl.toml schema, defaults, validation, discovery, save
internal/remote/remote.go     ssh (ControlMaster), remote Run/Output, rsync Pull, shell quoting
internal/xcode/xcode.go       xcodebuild build/-list/-showBuildSettings on the remote; locating the .app
internal/xcode/tuist.go       Tuist detection, tuist install/generate on the remote
internal/sim/sim.go           Local simctl wrapper: list/resolve/boot/install/launch, plutil bundle id
```

Keep this split. Remote shell work belongs in `internal/remote` and `internal/xcode`. Local `xcrun` and `plutil` calls belong in `internal/sim`. Orchestration and user-facing output belong in `main.go`.

## Commands

| Command | What it does |
|---|---|
| `init` | Runs Tuist prepare if needed, discovers the workspace/project and schemes on the remote, writes `.remotectrl.toml` (or `--global`). |
| `run` | Build remotely, sync the `.app`, install, and launch. |
| `sync` | Sync the last remote build, install, and launch. No build. |
| `build` | Build remotely only. |
| `doctor` | Checks local tools (`ssh`, `rsync`, `plutil`), a booted simulator, config, SSH, remote `xcodebuild`/`rsync`/`xcbeautify`/Tuist, and that the scheme builds an iOS app. |
| `list [devices\|schemes]` | Lists local simulators and remote schemes. Marks what the current config selects. |

Run `remotectrl help` for the usage text. The usage string in `main.go` is the source of truth for the command list.

## Configuration

- Project file: `.remotectrl.toml`, found by walking up from the cwd. Fallback: `~/.config/remotectrl/config.toml`.
- Precedence: CLI flags override the config file (`overrides.merge`). Defaults are applied afterwards (`ApplyDefaults`), then validated.
- Required keys: `host`, `dir`, `scheme`.
- `workspace` and `project` are mutually exclusive. Setting a flag for one replaces both.
- `target` is required when a scheme builds more than one app target.
- `configuration` defaults to `Debug`. `simulator` defaults to `booted` (accepts a name or UDID). `arch` defaults to this Mac's arch (`arm64` or `x86_64`). This is passed as `ARCHS=` with `ONLY_ACTIVE_ARCH=NO`, so simulator builds don't compile every arch.
- `.remotectrl.toml` is gitignored. Don't commit it. The `/remotectrl` binary is also gitignored.

## Architecture notes

These are load-bearing. Read them before changing the related code.

- **Multiplexed SSH.** `remote.New` puts all ssh and rsync traffic on one ControlMaster socket at `/tmp/remotectrl-<uid>/%C` with `ControlPersist=30m`. This keeps Tailscale check-mode and passphrase prompts to one per window. `Connect` runs `ssh -fN` attached to the terminal so interactive auth is visible. The socket path is under `/tmp` because the Unix socket path limit (~104 bytes) is exceeded by `os.TempDir()` on macOS.
- **Remote commands run in a login zsh.** `Remote.command` wraps scripts as `zsh -lc <quoted>` so Homebrew and other PATH entries match an interactive shell. Quote every interpolated value with `remote.Quote`. For user-supplied remote directories, use `remote.DirExpr`, which expands a leading `~` to `$HOME`.
- **No `-derivedDataPath`.** `xcode.Build` deliberately never passes it, so builds share Xcode's default DerivedData cache. The product is located afterwards with `xcodebuild -showBuildSettings -json` (`xcode.Locate`). Don't hardcode build paths.
- **App selection.** `Locate` keeps only targets whose `PRODUCT_TYPE` is `com.apple.product-type.application` and whose `WRAPPER_EXTENSION` is `app`. The remote path is `TARGET_BUILD_DIR/FULL_PRODUCT_NAME`.
- **Tuist.** A directory is a Tuist project if it has `Project.swift`, `Workspace.swift`, or `Tuist.swift`. Tuist-generated workspaces are usually gitignored and stale, so `xcode.Build` runs `tuist install` (only if `Tuist/.build` is missing or forced) and `tuist generate --no-open` before building. Tool resolution goes through `mise exec` when a mise config exists, otherwise `tuist` must be on the remote PATH. `--no-generate` and `--tuist-install` override this.
- **Local mirror and sync.** `main.go:localAppDir` returns `/tmp/remotectrl/<host>-<hash>/`, where `<hash>` is 8 hex chars of SHA-1 over `dir + "\x00" + scheme`. `remote.Pull` runs `rsync -a --delete` into that directory. Rebuilds transfer only changed files. Because the mirror is in `/tmp`, macOS periodic cleanup can remove it, and the next sync is then a full transfer.
- **Installing and launching.** `sim.Resolve` prefers a booted match and then the newest iOS runtime. `sim.BundleID` reads `CFBundleIdentifier` with `plutil` from the local `.app`. `sim.Launch` uses `--terminate-running-process`, and `--console` is added only with `run --console`.
- **Error handling.** Wrap errors with context using `%w` (for example, `fmt.Errorf("install: %w", err)`). Commands return errors to `main`, which prints `remotectrl: <err>` and exits 1. `doctor` tracks failures with a `check` helper and exits non-zero when any check fails.
- **Output.** Progress uses `step()`. Color is used only when stdout is a TTY and `NO_COLOR` is unset. Keep piped output free of escape codes.

## Development

Build and vet (no Makefile):

```sh
go build ./...
go vet ./...
go build -o remotectrl .
```

Smoke-test against a real Mac. Most behavior requires SSH to the remote and a local simulator:

```sh
./remotectrl doctor
./remotectrl list
./remotectrl run
```

There is no test suite yet. Verify changes by running the affected command end to end. For remote-only changes, `./remotectrl build` plus `./remotectrl doctor` is the minimum. For local sim changes, run `./remotectrl sync` against an existing remote build.

## Conventions

- Standard library first. The only third-party dependency is TOML parsing.
- Keep the CLI surface small. Add a flag only when a command needs it. Shared flags go through `overrides.bind`. Build flags go through `bindBuild`.
- Don't add a new convention next to an existing one. Reuse `remote.Quote`, `remote.DirExpr`, `step()`, and the `check`/`probe` helpers in `doctor`.
- Don't add shims or compatibility aliases when changing a function. Update all callers.
- Comments explain why, especially for non-obvious constraints like the socket path length, the lack of `-derivedDataPath`, and `ONLY_ACTIVE_ARCH=NO`. Keep them that way.
- Keep user-facing error messages actionable. Several existing messages name the fix (for example, "set `target` in config" or "run `remotectrl init`").

## Gotchas

- Changing `localAppDir`'s key or path orphans the existing mirror. It is harmless, but the next sync is a full transfer.
- `Pull` escapes spaces in the remote path but relies on the rest being safe. Don't pass unquoted remote paths.
- `xcodebuild -showBuildSettings` output can have login-shell noise before the JSON. `Locate` and `Schemes` skip to the first `[` or `{`. Keep that tolerance if you change the parsing.
- `xcbeautify` is optional on the remote. The build falls back to raw `xcodebuild` output when it is missing.
- `sim.Resolve("booted")` only matches a booted device. Other values match by UDID or case-insensitive name.
