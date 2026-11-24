# Ferry Go v2 fleet deployment — 23 November 2026

Built from clean `feat/go-v2-backend` commit `282dee7cf9ac2f6c91e451dbdc0f0c26a3dfd804`. No push, merge, or branch-protection changes.

`make build`, `make cross`, and `make app` passed with host Go/Swift cache access. Native app ad-hoc signature verified with `codesign --verify --deep --strict`. The installed bundled binary matches `dist/Ferry.app` byte for byte and embeds the pinned clean VCS revision. Signing changes its hash relative to the unsigned CLI. This revision uses `ferry version`; `--version` is unsupported.

| Host | OS/arch | Legacy removed | CLI location | Daemon / doctor |
|---|---|---|---|---|
| laptop | darwin/arm64 | Electron `~/Applications/ferry.app`; v0.3 editable CLI | `/opt/homebrew/bin/ferry`, `~/.local/bin/ferry` symlink | LaunchAgent running / pass |
| lunar01 | darwin/arm64 | Two v0.3 editable CLIs | `/opt/homebrew/bin/ferry` | LaunchAgent running / pass |
| lunar02 | darwin/arm64 | v0.3 editable CLI and old `~/bin/ferry` wrapper | `~/bin/ferry`, `~/.local/bin/ferry` symlink | LaunchAgent running / pass |
| titus | linux/amd64 | None found | `~/.local/bin/ferry` | systemd user enabled, active, linger / pass |
| kes | linux/arm64 | None found | `~/.local/bin/ferry` | systemd user enabled, active, linger enabled / pass |
| mackbox | linux/amd64 | None found | `~/.local/bin/ferry` | systemd user enabled, active, linger / pass |
| lunar04 | skipped | Connection timeout | — | — |
| llms01 | skipped | DNS failure | — | — |
| llms02 | skipped | DNS failure | — | — |
| llms04 | skipped | DNS failure | — | — |
| vulcan | skipped | DNS failure | — | — |
| mackjr | skipped | Connection timeout | — | — |
| titus-win | skipped | Connection timeout | — | — |
| henry-slate | skipped | SSH forced agent command; no shell | — | — |

All six deployed CLI and live daemon versions are `282dee7` (Go 1.27.4). Fresh SSH sessions resolve the new CLI. All six accept IPv4 and IPv6 loopback TCP connections on port 9410 using the documented default `:9410` listen address. No transfers were active during deployment. No roots or peer pairings were configured previously; new configurations retain no exported roots. No end-to-end media transfer or new pairing was attempted.

## Preserved data

On laptop, lunar01 and lunar02, the incompatible database at `~/Library/Application Support/ferry/ferry.db` was backed up using SQLite's online backup API to `ferry.db.v0.3.bak`, and the backup passed `PRAGMA integrity_check`. Original database and WAL/SHM siblings were retained as `*.v0.3.original.bak`. Receipts and other application data were retained. No existing identity/pairing key was removed or replaced. All six daemon identity keys were first-created during this deployment. No user media was touched.

Removed registered v0.3 distributions using their RECORD manifests, constrained to installation prefixes. Source checkouts, historical build artifacts and rollback source snapshots were preserved. Audit covered SSH-account install/environment locations, PATH binaries and system/user service definitions; inaccessible accounts were not inspected.

## Laptop app and Park

`~/Applications/Ferry.app` installed after completely removing lowercase Electron `ferry.app`. Codesign, bundled revision, artifact comparison and running app process verified. Visual acceptance was not performed.

Park's deployed Ferry adapter was old. Replaced `~/.local/lib/lp-ferry` with an adapter-only deployment from the pinned revision; preserved previous deployed source at `~/.local/share/ferry/deployment-backups/lp-ferry.v0.3.bak`, outside plugin discovery. Verified no running/leased laptop jobs before reloading `com.lunarpark.park-node`. Park's deployed loader registers 20 Ferry ops without errors, resolves `/opt/homebrew/bin/ferry` under its LaunchAgent PATH, and reads the live daemon peer identity.

Known separate issue: Park's source app catalog still requires bundle ID `io.github.dspury.file-ferry`, whereas native Ferry uses `dev.lunarpark.ferry`. The app-opening shortcut can fail discovery even though the backend plugin works. Park.app was not rebuilt or modified. Remote Park adapters/hub were not upgraded; this deployment installs the remote Go daemons and upgrades only the requested laptop Park adapter.

## Evidence and remaining work

`ferry-host-probe.json` records every SSH-config alias with five-second connect timeout and batch authentication; `ferry-verification.json` records final live versions, paths, checks, daemon states and installed-binary hashes. Logs from the operational scripts remain in `/tmp/ferry-{audit,deploy}-*.txt` for this session.

Reconnect skipped hosts to deploy there. Before peer transfers, configure explicit roots and pair the intended peers. Update Park's Ferry app catalog in a separately scoped Park build if its launcher is needed.
