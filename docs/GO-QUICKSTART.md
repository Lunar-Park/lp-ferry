# Ferry v2 quickstart

Ferry v2 is one static binary, `ferry`. It runs as a background daemon (`ferry daemon`), and everything else (the CLI, `ferry mcp` for agents, the macOS app) talks to that daemon over a user-only socket. It copies and verifies. It never deletes, moves or overwrites.

## Install

Build from source with Go 1.26+ (no C toolchain needed):

```sh
make build                 # bin/ferry for this machine
make cross                 # bin/ferry-{linux,darwin}-{amd64,arm64}
install -m 0755 bin/ferry /usr/local/bin/ferry
```

`ferry version` prints the build.

## Configure

Copy [`packaging/ferry.toml.example`](../packaging/ferry.toml.example) to `~/.ferry/ferry.toml` and edit it. Unknown keys stop the daemon and are named in the error, so a typo never silently becomes a default.

- `[[roots]]` are the only folders paired machines and agents can reach: `ro` to read from, `rw` to write into. Leave them out and nothing is exported.
- `[agent_policy]` decides what an agent may start without you (see [Agents](#agents-mcp)).

Check the setup at any time, with or without the daemon running:

```sh
ferry doctor
```

It reports runtime, config and key permissions, the database (WAL), socket mode, physical readback mode, each network path with its tier, and for every root: whether it exists, whether it is writable, which no-clobber publish method it supports, and free space.

## Run the daemon

**macOS (per-user LaunchAgent):**

```sh
cp packaging/launchd/com.lunarpark.ferryd.plist ~/Library/LaunchAgents/
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.lunarpark.ferryd.plist
```

**Headless Linux (systemd, dedicated `ferry` user):** follow the install comment at the top of [`packaging/systemd/ferryd.service`](../packaging/systemd/ferryd.service). The unit is `Type=notify` and hardened (`ProtectSystem=strict`). Add every `rw` root to `ReadWritePaths=`, and run the CLI as that user (`sudo -u ferry ferry status`).

**Foreground, for a quick try:** `ferry daemon` (add `--no-mdns` to skip Bonjour, or `--no-p2p` for local-only).

## Copy and verify

```sh
ferry plan /Volumes/CARD --to /Volumes/Shuttle/Ingest --preset camera-card-conform --role CARD=ACAM
ferry approve <plan_id> --fingerprint <fingerprint>     # exactly what you reviewed
# or in one step:
ferry send /Volumes/CARD --to /Volumes/Shuttle/Ingest --to /Volumes/Backup --yes
```

- **Several destinations:** each extra `--to` is a simultaneous destination fed from a single read of the source.
- **Destination forms:** a path, `@<saved destination id>`, or `<peer_id>:/path` on a paired machine.
- **Before anything copies:** `ferry plan` shows findings, conflicts and per-destination space. Approval refuses a plan with blocking items, and re-checks that nothing changed since you reviewed it.
- **Every file is checked before it appears:** it is written to a temp file, fsynced, read back from disk with the cache bypassed, hash-compared, and only then published without replacing anything.

Watch progress with `ferry status <transfer_id> --watch`, or `ferry events` for everything. `ferry cancel` and `ferry resume` are safe: verified files are kept and never re-copied.

**Presets:** `ferry preset list`, `ferry preset preview sort-by-category Photos/IMG_1.JPG`, `ferry preset export <id>`. Save destinations with their storage identity using `ferry dest save Shuttle /Volumes/Shuttle --preset preserve-source-structure`. Teach Ferry a camera's role once with `ferry camera bind Sony ILME-FX3 <serial> ACAM`.

### Verify later (cron-friendly)

```sh
ferry verify --transfer <transfer_id>        # via the daemon; peers verify their own copies
ferry verify --receipt ~/.ferry/receipts/<receipt_id>.json --root /Volumes/Shuttle/Ingest
```

| exit code | meaning |
|---|---|
| 0 | clean |
| 1 | a file is missing |
| 2 | a file was modified |
| 3 | a file was added |
| 4 | could not verify (unreadable file, unreachable folder, or an edited receipt) |

Code 4 outranks the others.

## Two machines

On the receiving machine:

```sh
ferry peer pair        # prints a single-use code, valid 5 minutes
```

On the sending machine:

```sh
ferry peer pair 'ferry://pair/fry_…?code=…&ep=…'
ferry send /Volumes/CARD --to /Volumes/SSD --to fry_<server>:/mnt/studio-nas/Ingest --yes
```

- **Trust:** pairing pins each machine's Ed25519 key, and every later connection is mutual TLS 1.3 against those pinned keys. There is no cloud account and no relay.
- **Direct transfer:** bytes go straight from the source machine to the destination. When the source is a remote peer, the destination pulls from it directly.
- **Path choice:** Ferry probes every path it knows (direct link, LAN, Wi-Fi, Tailscale), uses the fastest, and fails over mid-file, resuming at the exact byte offset.
- **Access control:** the receiving machine only accepts writes into its `rw` roots.
- **Path management:** `ferry peer list`, `ferry peer probe <peer>` (shows the paths and the chosen one), `ferry peer remove <peer>`.

## Scripting an agent: `--json`, `--actor`, exit codes

Any subcommand takes `--json`, and with it a failure is one object on stderr rather than a
sentence:

```sh
ferry send /Volumes/CARD --to @ssd --json
{"error": {"code": "usage", "message": "ferry send: required flag(s) \"to\" not set"}}
```

The exit code says what kind of failure it was, so a caller never has to parse English:

| code | meaning |
|------|---------|
| 0 | ok |
| 1 | refused: the daemon said no, a plan was blocked, the work failed |
| 2 | usage: unknown flag, missing or extra argument |
| 3 | environment: no daemon at the socket, or a config that cannot be read |

`ferry verify` keeps its own codes `0`-`4` (clean, missing, modified, added, unreadable); it
is a cron check, and its `--json` errors name the category (`verify_modified`, …).

`ferry send --json` without `--yes` answers with the plan it built, so a program can approve
or report it:

```json
{"plan_id": "pln_…", "fingerprint": "…", "status": "awaiting_approval", "blocking": 0, "summary": {…}}
```

`status` is `planned` when the plan has blocking items; the command then exits `1` with
`plan_blocked`, and the same plan object is still on stdout.

**Who the call is from.** `--actor NAME` (or `FERRY_ACTOR`, then `PARK_ACTOR`) names the
actor a plan is made by. A bare name becomes `agent:NAME`, which is what makes the daemon
apply `[agent_policy]` to it instead of a human's allowances — an agent that forgets the flag
is on the record as `human`. `--actor human` says D.

## Agents (MCP)

`ferry mcp` is an MCP server over stdio that talks to the running daemon. For Claude Code:

```sh
claude mcp add ferry -- ferry mcp
```

Any other MCP client takes `{"command": "ferry", "args": ["mcp"]}`.

**What agents can do:** the agent gets twelve tools: status, drives, peers, destinations, presets, plan, plan tree, transfer start/status/cancel, receipt and verify. None of them deletes, moves or overwrites anything. Every action is recorded as `agent:<client name>`, and the agent's transfers show live in `ferry events` and the app.

**What waits for you:** under `[agent_policy]`, a transfer to an unsaved destination, or one larger than `auto_approve_max_bytes`, is held as `awaiting_approval`; `ferry status` lists held plans. It starts, still attributed to the agent, when you run `ferry approve <plan_id> --fingerprint <fp>`. Agents cannot approve.

The socket protocol (for the macOS app and other clients) is documented in [`IPC-PROTOCOL.md`](IPC-PROTOCOL.md).

## Releases

`.goreleaser.yaml` builds static `linux`/`darwin` × `amd64`/`arm64` archives, which include this guide, the example config and both service files. Go releases are tagged `v2.*`; `v0.*` tags belong to the legacy Python app.

## Standalone macOS app

On macOS 14 or later with Go and the Xcode Swift toolchain, run `make app`
and open `dist/Ferry.app`. The bundle contains the daemon, Inter and the app
icon. The default signature is ad hoc. Set `FERRY_SIGN_IDENTITY` to select
a signing identity. Ferry connects to `~/.ferry/ferry.sock` and starts its
bundled daemon when the socket is unavailable. Set `FERRY_SOCKET` to use
an alternate socket.

Run `make app-test` for Swift model, reducer and scratch-daemon tests.
Launch the bundle executable with `FERRY_FIXTURE=1` for an isolated sample
workspace. Dashboard opens with connected volumes, paired machines and transfer history.
To capture all six fixture views at the two review widths, run:

```sh
FERRY_FIXTURE=1 FERRY_SNAPSHOT_DIR="$PWD/dist/screens" dist/Ferry.app/Contents/MacOS/Ferry
FERRY_FIXTURE=1 FERRY_SNAPSHOT_WIDTH=800 FERRY_SNAPSHOT_DIR="$PWD/dist/screens-compact" dist/Ferry.app/Contents/MacOS/Ferry
```
