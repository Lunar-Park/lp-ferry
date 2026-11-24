# ferry

[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/Lunar-Park/lp-ferry/ci.yml?style=flat-square)](https://github.com/Lunar-Park/lp-ferry/actions/workflows/ci.yml)

Verified, local-first file transport for people and agents. It moves a camera card to an SSD and a NAS at once, an SSD to another SSD, or a folder on one machine straight to another machine's disk. Every file is proven to have arrived byte for byte, and every transfer ends with a receipt you can re-check later.

`ferry` is one static binary for Linux and macOS:

```text
ferry daemon   the engine: disk I/O, transfers, machine-to-machine transport, ledger, local socket
ferry <cmd>    the CLI (plan, approve, send, status, verify, peer, dest, preset, doctor, …)
ferry mcp      an MCP server, so agents can plan and run transfers that show up live for you
```

## What it guarantees

- **Copy only.** Ferry never deletes a source and never replaces an existing file. A name that is taken gets a deterministic `name (2).ext` or a skip proven by full checksum, never an overwrite.
- **Verified on the disk, not in memory.** Each file is written to a job-owned temp file and fsynced. It is then read back from the drive with the page cache bypassed (`F_NOCACHE` on macOS, `FADV_DONTNEED` on Linux) and hash-compared. Only then is it published, with an atomic no-replace primitive probed for that destination.
- **One reviewed plan, exactly.** Transfers start from an approved plan fingerprint. Changing the source, destination, preset or a camera role invalidates the approval.
- **Fails closed.** Symlinks under a root, a share that unmounted behind its mount folder, unknown config keys, scan errors and stale approvals all stop the transfer loudly.
- **Direct between machines.** Two machines pair once and pin each other's Ed25519 keys. After that every connection is mutual TLS 1.3, and the bytes travel the fastest direct path (direct link, LAN, Wi-Fi or Tailscale), with failover that resumes at the exact byte. There is no cloud, no relay, and nothing staged on a third machine.
- **Receipts.** `ferry-receipt-v2` records every file's hashes, where it landed, how it was verified and published, and a tamper-evident digest. `ferry verify` re-proves a receipt at any time, with cron-friendly exit codes.

## Quick start

```sh
make build && install -m 0755 bin/ferry /usr/local/bin/ferry
ferry daemon &                       # or install the launchd / systemd service
ferry send /Volumes/CARD --to /Volumes/SSD/Ingest --to /Volumes/NAS/Ingest --yes
ferry verify --transfer <transfer_id>
```

[docs/GO-QUICKSTART.md](docs/GO-QUICKSTART.md) covers:
- installation and the headless Linux service
- configuration (`~/.ferry/ferry.toml`)
- presets and camera roles
- pairing two machines
- connecting an agent (`claude mcp add ferry -- ferry mcp`)

## Organizing on the way in

Presets route files as they land. The built-in presets are:
- `preserve-source-structure`
- `mirror-source`
- `sort-by-category`
- `camera-card-conform`

For example, `{camera_role}/{capture_date}/{camera_role}_{filename}` files a Sony card bound as ACAM under `ACAM/2026-11-22/ACAM_C0001.MP4`, and its XML sidecar goes with it.

Capture dates and camera identity come from container headers, read in pure Go (MP4/MOV, EXIF, BWF/iXML, Sony XML), with no FFmpeg. A file with no capture date is routed to the fallback with a warning; Ferry never uses "today" in its place.

Every plan can be inspected before anything copies, as a paged before → after tree (`ferry_plan_tree` for agents, `plan.tree` over the socket).

## For apps and agents

- **Apps:** the daemon speaks newline-delimited JSON-RPC 2.0 on a user-only Unix socket. Live `transfer.progress` events (every 150 ms, with an honest two-phase ETA) carry who started the transfer. The contract is [docs/IPC-PROTOCOL.md](docs/IPC-PROTOCOL.md); it's what the native macOS app binds to.
- **Agents:** they get twelve MCP tools, none of which can delete, move or overwrite anything. Under `[agent_policy]`, a transfer to an unsaved destination or over a size limit waits for a human `ferry approve`.
- **Park:** `park_plugins/ferry/` keeps Park's ingest workspace, records and project filing, and delegates every byte to the local daemon ([docs/FERRY-PLUGIN.md](docs/FERRY-PLUGIN.md)).

## Repository

| Path | What |
|---|---|
| `cmd/ferry` | the binary: CLI, `daemon`, `mcp` |
| `internal/` | the engine: `storage` (containment, no-replace publish, physical readback), `engine` (single-read fan-out, crash recovery, telemetry), `planner`, `preset`, `metadata`, `identity`, `ledger` (SQLite WAL), `p2p`, `daemon`, `ipc`, `mcp`, `policy`, `receipt`, `verify` |
| `park_plugins/ferry` | the Park adapter (Python, standard library only) |
| `packaging/` | systemd unit, launchd agent, example config |
| `docs/` | quickstart, IPC protocol, plugin, the rewrite plan and the behavioral specs |

Run `make vet lint race cross` to vet, lint, run the race-enabled tests and cross-build. The architecture and phase gates are in [docs/GO-BACKEND-REWRITE-PLAN.md](docs/GO-BACKEND-REWRITE-PLAN.md).

The Python CLI/TUI and the Electron desktop app this replaced are preserved at the `legacy-python-v0.3` tag.

## License

MIT. See [LICENSE](LICENSE).
