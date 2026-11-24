# Ferry v2 — Go Backend Rewrite & Open-Source Architecture Plan

> **Status:** Architecture & Execution Blueprint (handoff-ready for implementation)  
> **Date:** 2026-11-22  
> **Scope:** Complete Go backend (`ferry` single binary: headless Linux + macOS daemon, CLI, MCP server, P2P transport, multi-destination verified copy engine, presets/renaming/camera metadata, SQLite ledger, and Unix-socket IPC for the native SwiftUI macOS app).  
> **Non-scope:** SwiftUI visual design/styling (owned separately under the Park/Slate style system; this document specifies the exact IPC & telemetry contract the SwiftUI app consumes).

---

## 1. Product Vision & Non-Negotiable Invariants

Ferry is an open-source, local-first, peer-to-peer **verified file transport and organization engine** built for both **humans** (via a native SwiftUI macOS app + CLI) and **AI agents** (via MCP + JSON CLI).

Its job is to make moving files—camera card to SSD + NAS simultaneously, SSD to SSD, Mac to Linux server over direct Ethernet/10GbE/switch/Tailscale—**fast, visually predictable, cryptographically proven, and safe for agents to invoke autonomously**.

### Hard Invariants (Carried Forward & Hardened)

Every invariant proven in [`DESTINATION-PRESETS-PRODUCTION-SPEC.md`](DESTINATION-PRESETS-PRODUCTION-SPEC.md) and [`MACHINE-TO-MACHINE-SEND.md`](MACHINE-TO-MACHINE-SEND.md) remains law in the Go rewrite:

1. **Copy-only, never destructive by default.** Ferry never deletes source files and never silently overwrites an existing destination file.
2. **Direct A → B, never staged on a third machine.** Bytes stream directly from source peer to destination peer over the fastest available direct link. No cloud, no hub relay, no intermediate disk staging.
3. **True byte-for-byte verification with disk readback.** Writing to the OS page cache is not proof of durability. Verification flushes (`fsync`) and forces physical readback (`F_NOCACHE` on macOS, `FADVISE_DONTNEED` / `O_DIRECT` on Linux) before atomic publication.
4. **One front door for every workflow.** Local drive-to-drive, simultaneous multi-destination fan-out, and cross-machine sends all flow through one unified pipeline:
   `Inventory → Plan → Review/Approve (exact fingerprint) → Execute → Verify → Receipt`.
5. **Explicit fail-closed safety.** Symlink traversal, unmounted network share backing-disk traps, unknown config keys, scan errors, and stale approvals all fail loudly—never silently degrade.
6. **Public-repo guardrail.** Nothing in this repo may contain private hostnames, personal NAS paths, credentials, or client media paths. Do not push unpublished branches to GitHub until the maintainer explicitly approves.

---

## 2. Repository Triage: What to Remove, Keep, and Rewrite

Do **not** delete the Python/Electron codebase on day one. Tag the current state first (`git tag legacy-python-v0.3`) and build the Go engine cleanly alongside it until Phase 4 passes acceptance gates, then retire the legacy layers in Phase 5.

### 2.1 Remove (Retire in Phase 5 after Go parity)

| Path | What it is today | Why it goes |
|---|---|---|
| `desktop/` | Electron + React + Vite + Node 9-screen shell | Replaced by a native SwiftUI macOS app talking to the Go daemon over Unix socket. |
| `src/file_ferry/` | Python CLI, Textual TUI, vNext `ApplicationService`, SQLite migrations, stdio JSON-RPC sidecar | Replaced by the single-binary Go engine (`cmd/ferry` + `internal/`). Eliminates PyInstaller bloat (~100 MB), GIL fan-out limits, and Linux Python venv friction. |
| `tests/` (excluding `tests/park_plugin/`) | Python test suite for `src/file_ferry/` | Port all behavioral invariants (acceptance matrix `A01–A26`, `E01–E16`) into Go `*_test.go` suites with `-race`. |
| `scripts/build-sidecar.sh`, `scripts/sidecar.spec`, `scripts/package-mac-local.sh`, `scripts/package-release.sh`, `scripts/verify-packaged.sh`, `scripts/stamp-release.js`, `scripts/d2_metrics.py` | PyInstaller & Electron packaging/benchmark harness | Replaced by `Makefile` + `.goreleaser.yaml` + static Go cross-builds + native macOS helper bundling. |
| `pyproject.toml`, `build/`, `.venv/` | Python package metadata & local build artifacts | Replaced by `go.mod` / `go.sum` (keep a minimal `park_plugins/ferry/pyproject.toml` only if Park installs the thin adapter as a Python package). |
| `docs/UI-REVISION-SPEC.md`, `docs/FILE-FERRY-FULL-APP-IMPLEMENTATION-PLAN.md`, `docs/PACKAGING-HANDOFF.md`, `docs/CLI-TUI-PARITY.md`, `docs/a11y/`, `docs/design/`, `docs/ui-refs/` | Electron reskin & legacy packaging docs | Obsolete once Electron is retired (still preserved in git tag `legacy-python-v0.3`). |

### 2.2 Keep & Adapt

| Path | Action |
|---|---|
| `park_plugins/ferry/` + `tests/park_plugin/` | **Keep working during Phases 0–4**, then convert in Phase 5 into a **thin Python adapter** (~200 LOC) that calls the local `ferry` Go daemon/CLI over Unix socket or JSON CLI, preserving Park's `ferry.*` op contract without duplicating copy, preset, or P2P logic. |
| `docs/DESTINATION-PRESETS-PRODUCTION-SPEC.md`, `docs/ORGANIZATION-PRESET-EXAMPLES.md`, `docs/MACHINE-TO-MACHINE-SEND.md` | **Keep as authoritative behavioral specs** (move under `docs/specs/` if desired). Every `A01–A26` and `E01–E16` test case must be covered in Go. |
| `src/file_ferry/native/avproxy.swift` | Optional reference if macOS AVFoundation proxy generation is offered later as a separate post-transfer plugin; out of scope for the core Go transfer daemon. |
| `LICENSE`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `.gitleaks.toml`, `.github/workflows/secret-scan.yml`, `scripts/secret-scan.sh`, `assets/brand/` | **Keep unchanged.** |
| `.github/workflows/ci.yml` | **Rewrite** for Go (`go test -race`, `golangci-lint`, cross-compile verification for `linux/amd64`, `linux/arm64`, `darwin/arm64`). |

---

## 3. Target Go Architecture & Package Layout

### 3.1 Toolchain & Dependency Policy

- **Language:** Go `1.24+`, module `github.com/Lunar-Park/lp-ferry`.
- **Static Builds (`CGO_ENABLED=0`):** Every release target (`linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64`) compiles from macOS or Linux with zero C toolchain dependencies. This makes Linux NAS/server deployment a single ~15 MB binary and makes embedding `ferry` inside a signed/notarized macOS SwiftUI `.app` bundle trivial.
- **Minimal, permissive-license dependencies only (MIT / BSD / Apache-2.0):**
  - **SQLite:** `modernc.org/sqlite` (pure Go, zero CGO; WAL mode + single-writer queue prevents `SQLITE_BUSY`).
  - **Hashing:** stdlib `crypto/sha256` (hardware SHA-NI / ARMv8 Crypto extensions) as canonical identity + `github.com/zeebo/blake3` (SIMD multi-GB/s optional fast mode). Always record `checksum_algo` explicitly in receipts.
  - **CLI:** `github.com/spf13/cobra` (clean subcommands, `--json` output everywhere, shell completions).
  - **Config:** `github.com/BurntSushi/toml` (strictly reject unknown keys via `md.Undecoded()`).
  - **Networking / P2P:** Go stdlib `crypto/tls` (TLS 1.3 + Ed25519 mutual TLS) + `golang.org/x/net/http2` (multiplexed streams over TCP—takes full advantage of kernel TSO/GRO offload on 10GbE Ethernet, outperforming userspace UDP/QUIC on LAN).
  - **mDNS / Bonjour Discovery:** `github.com/grandcat/zeroconf` (or `github.com/hashicorp/mdns`) advertising `_ferry._tcp` per active interface.
  - **MCP Server:** `github.com/modelcontextprotocol/go-sdk` (or lean JSON-RPC 2.0 stdio implementation if SDK stability warrants zero extra deps).
  - **Low-level OS Syscalls:** `golang.org/x/sys/unix` (`fcntl(F_NOCACHE)`, `Fadvise`, `Renameat2`, `Statfs`, `Lstat`, `Link`).

### 3.2 One Binary, Three Operating Modes

```text
ferry daemon   ──► Headless background engine (systemd on Linux, launchd or embedded helper on macOS)
                   Owns: disk I/O, P2P listener/discovery, transfer queue, SQLite ledger, Unix socket IPC
ferry <cmd>    ──► CLI client (talks to ~/.ferry/ferry.sock; supports standalone mode for one-shot verify)
ferry mcp      ──► MCP server over stdio (talks to ~/.ferry/ferry.sock; every agent transfer appears live in UI)
```

### 3.3 Repository Structure

```text
lp-ferry/
├── go.mod
├── go.sum
├── Makefile
├── .goreleaser.yaml
├── cmd/
│   └── ferry/
│       ├── main.go                 # Root cobra entry + global flags (--socket, --config, --json)
│       ├── daemon.go               # `ferry daemon` lifecycle, signals, systemd/launchd readiness
│       ├── mcp.go                  # `ferry mcp` stdio MCP bridge
│       ├── transfer.go             # `ferry plan`, `approve`, `send`, `status`, `cancel`, `resume`
│       ├── verify.go               # `ferry verify` (cron exit codes 0/1/2/3/4)
│       ├── peer.go                 # `ferry peer pair|list|probe|remove`
│       ├── drive.go                # `ferry drive list|watch`
│       ├── destination.go          # `ferry dest save|list|resolve|rebind|archive`
│       ├── preset.go               # `ferry preset list|show|save|import|export|preview`
│       └── doctor.go               # `ferry doctor` (env, link speeds, fs publish support, perms)
├── internal/
│   ├── config/                     # Strict ferry.toml loader (rejects unknown keys)
│   ├── model/                      # Canonical domain types (Location, Plan, Preset, Transfer, Receipt)
│   ├── policy/                     # Agent & peer access guardrails (allowed roots, auto-approve rules)
│   ├── ledger/
│   │   ├── db.go                   # SQLite WAL pool, serialized write tx, backup/recovery
│   │   ├── migrations/             # Embedded SQL forward migrations (0001_init.sql, ...)
│   │   └── repo_*.go               # Repositories: peers, destinations, presets, inventories, plans, transfers, receipts
│   ├── identity/
│   │   ├── probe.go                # Platform-agnostic VolumeIdentity & MountObserver interfaces
│   │   ├── probe_darwin.go         # diskutil plist + statfs + /Volumes watcher + link speed
│   │   ├── probe_linux.go          # /proc/self/mountinfo + lsblk JSON + ROTA/TRAN + sysfs link speed
│   │   ├── share.go                # SMB/NFS device sanitizer (strips user:pass@ credentials)
│   │   └── resolver.go             # available / offline / needs_confirmation / ambiguous / unwritable
│   ├── metadata/
│   │   ├── camera.go               # Pure-Go camera card & recorder fingerprinting (Sony/RED/ARRI/BMD/Canon/DJI/GoPro/BWF)
│   │   ├── probe.go                # Pure-Go fast MP4/MOV/EXIF/WAV(BWF) header reader (no FFmpeg required)
│   │   └── ffprobe.go              # Optional ffprobe enrichment adapter
│   ├── preset/
│   │   ├── engine.go               # Deterministic rule evaluator, keep-together groups, exclusions
│   │   ├── template.go             # Path + filename renaming templates ({camera_role}, {capture_date}, {seq}, etc.)
│   │   ├── categories.go           # Versioned extension → category map (v1)
│   │   ├── builtins.go             # Starter presets: preserve-source, sort-by-category, camera-card-conform
│   │   └── service.go              # Immutable revisions, content hashes, portable JSON import/export
│   ├── planner/
│   │   ├── scanner.go              # Full inventory walker (lstat, empty dirs, scan errors recorded, never capped)
│   │   ├── conflicts.go            # Same-name/diff-content, case/Unicode NFC/NFD, ancestor, group-root conflicts
│   │   ├── capacity.go             # Per-destination capacity (planned + temp sibling + reserve)
│   │   ├── planner.go              # Unified multi-dest + local/remote planner + deterministic plan fingerprint
│   │   └── tree.go                 # Server-side Before→After trie projection for instant UI visualization
│   ├── storage/
│   │   ├── containment.go          # lstat component-by-component containment + symlink-escape refusal
│   │   ├── publish.go              # PublishStrategy probe & atomic no-clobber publication
│   │   ├── publish_darwin.go       # Hardlink + renameatx_np(RENAME_EXCL) + reserve_rename fallback
│   │   ├── publish_linux.go        # Hardlink + renameat2(RENAME_NOREPLACE) + reserve_rename fallback
│   │   ├── readback_darwin.go      # F_NOCACHE physical readback reader
│   │   └── readback_linux.go       # FADV_DONTNEED / O_DIRECT physical readback reader
│   ├── engine/
│   │   ├── bufferpool.go           # sync.Pool of 2 MiB aligned buffers (near-zero GC pressure)
│   │   ├── fanout.go               # Single-read → N-sink bounded ring pipeline (local + remote sinks)
│   │   ├── scheduler.go            # Adaptive concurrency (HDD vs NVMe vs SD card vs 10GbE)
│   │   ├── telemetry.go            # Dual-phase EMA throughput + honest ETA calculator
│   │   ├── runner.go               # Durable execution, crash-window reconciliation, chunk-level cancel, resume
│   │   └── reservations.go         # Cross-job destination path reservation table
│   ├── p2p/
│   │   ├── identity.go             # Ed25519 keypair + self-signed mTLS 1.3 leaf cert + PeerID
│   │   ├── pairing.go              # Short-lived pairing tokens / SAS verification + peer pinning
│   │   ├── grant.go                # Per-transfer signed capability grants (bound to transfer + peer + items + TTL)
│   │   ├── discovery.go            # mDNS (_ferry._tcp) + local NIC speed + Tailscale IP discovery
│   │   ├── pathradar.go            # Parallel endpoint racing, RTT/bandwidth probe, tier selection & failover
│   │   ├── server.go               # mTLS HTTP/2 file & coordination server (strict root + grant enforcement)
│   │   └── client.go               # mTLS HTTP/2 client (no redirects, Range resume, streaming push/pull)
│   ├── receipt/
│   │   ├── builder.go              # Cryptographic receipt builder + digest + JSON exporter
│   │   └── schema.go               # Portable ferry-receipt-v2 JSON schema
│   ├── verify/
│   │   └── verifier.go             # Independent local & remote receipt verification with disk readback
│   ├── daemon/
│   │   ├── daemon.go               # Wires all services, watchers, P2P server, scheduler, IPC hub
│   │   └── eventbus.go             # Fan-out pub/sub for live telemetry, drive events, peer events
│   ├── ipc/
│   │   ├── protocol.go             # Versioned JSON-RPC 2.0 + server-push frame codecs
│   │   ├── server.go               # Unix socket server (~/.ferry/ferry.sock, 0600)
│   │   └── client.go               # Go client used by CLI & MCP subcommands
│   └── mcp/
│       └── server.go               # MCP tool definitions & safe agent execution bridge
├── packaging/
│   ├── systemd/ferryd.service
│   └── launchd/com.lunarpark.ferryd.plist
├── park_plugins/ferry/             # Thin adapter to Go daemon (Phase 5)
└── docs/
```

---

## 4. Detailed Subsystem Design & Logic

### 4.1 Unified Location Model & Single Front Door (`internal/model`, `internal/planner`)

There is no separate "local offload" vs. "machine-to-machine send" API. Every source and destination is a `Location`:

```go
type Location struct {
    PeerID        string `json:"peer_id"`                  // "local" or pinned Ed25519 peer fingerprint
    Path          string `json:"path"`                     // Absolute path on that machine
    DestinationID string `json:"destination_id,omitempty"` // Optional saved destination reference
}

type PlanRequest struct {
    Sources      []SourceSpec      `json:"sources"`      // 1..N folders/drives on local or a source peer
    Destinations []DestinationSpec `json:"destinations"` // 1..N targets (e.g. local SSD + 10GbE Linux NAS)
    PresetID     string            `json:"preset_id,omitempty"`
    PresetRev    int               `json:"preset_rev,omitempty"`
    CameraRoles  map[string]string `json:"camera_roles,omitempty"` // source_id -> "ACAM", "BCAM", etc.
    Actor        string            `json:"actor"`        // "human" | "agent:claude" | "agent:codex"
}
```

**Why this matters:**
- An agent or user calls **one** operation (`plan.create`) whether moving files from SD card → SSD, SD card → SSD + Linux server simultaneously, or Mac SSD → Linux server.
- The planner inspects each source (locally via `scanner.go`, or remotely via `POST /v1/inventory` on the source peer), resolves each destination's identity/free space/publish strategy (locally or via remote peer), runs the preset & conflict engine for each target, and computes a single deterministic **Plan Fingerprint** (`SHA-256` of canonical JSON covering source manifest hashes, all destination mappings, destination identity snapshots, preset revision content hash, conflict policies, exclusions, and camera role overrides).
- Any change to source, destination, binding, preset, or conflict resolutions invalidates prior approvals in the **same SQLite transaction**.

---

### 4.2 High-Performance Verified Copy & Multi-Destination Fan-Out (`internal/engine`, `internal/storage`)

This is the heart of Ferry. The goal is to **saturate NVMe and 10GbE** while maintaining ironclad data integrity.

#### 4.2.1 Single-Read, N-Destination Pipeline (`fanout.go`)

When copying a camera card to an SSD **and** a Linux server over Ethernet at the same time:
1. Open the source file **once** (`O_RDONLY`, `F_NOCACHE` / `FADV_SEQUENTIAL` to avoid polluting host RAM with 500 GB of video footage).
2. Capture pre-read identity: `before := fstat(fd)` (`Dev`, `Ino`, `Size`, `Mtim`).
3. Pull a `2 MiB` buffer from `sync.Pool` (zero heap churn, invisible GC).
4. For each chunk read:
   - Check `ctx.Err()` (chunk-level cancellation — responds in milliseconds even on 100 GB ProRes files).
   - Update the source incremental hasher (`sha256.New()`, plus optional `blake3.New()`).
   - Increment refcount on the chunk buffer to `len(sinks)` and send its pointer into each sink's bounded channel (`cap = 4`, i.e., max 8 MiB buffered per destination).
   - Each sink goroutine writes the chunk to its destination (`localFileSink` or `remotePeerSink`), decrements the buffer refcount, and returns the buffer to `sync.Pool` when refcount reaches 0.
   - **Backpressure:** If Sink B (e.g., 1GbE NAS) is slower than Sink A (Thunderbolt NVMe), Sink B's bounded channel naturally paces the reader at 8 MiB max memory overhead. *Optional optimization flag (`decouple_slow_sinks`)*: if Sink A is >5× faster than Sink B and user enables fast-card-eject mode, Sink A finishes first so the card can be verified on SSD, while Sink B continues from SSD rather than holding the card hostage!
5. After EOF on source:
   - Re-`fstat(fd)` (`after`). If `(before.Dev, before.Ino, before.Size, before.Mtim) != (after.Dev, after.Ino, after.Size, after.Mtim)` → abort all sinks, clean up `.ferry-part` files, fail item with `ErrSourceChanged`.
6. Each sink independently completes **Durability → Physical Readback → Atomic Publication**:
   - `fsync(partFD)` and close.
   - Re-open `.ferry-part` with physical readback bypass:
     - **macOS (`readback_darwin.go`):** `unix.FcntlInt(fd, unix.F_NOCACHE, 1)` — forces read directly from drive/controller/SMB rather than Apple's unified buffer cache.
     - **Linux (`readback_linux.go`):** `unix.Fadvise(fd, 0, 0, unix.FADV_DONTNEED)` after `fsync`, then re-read (with optional `O_DIRECT` when block-aligned).
     - Record exact `readback_mode` (`"f_nocache"`, `"fadvise_dontneed"`, `"remote_readback"`) in the item ledger & receipt.
   - Hash the readback stream. If `destHash != sourceHash` → delete `.ferry-part`, mark target item `failed` with `ErrChecksumMismatch` (never publish!).
   - Re-check destination mount binding (`st_dev` + resolved root `lstat` containment) right before publication to close the unmounted-share race window.
   - Publish atomically via probed `PublishStrategy`:
     1. **`hardlink` (default):** `os.Link(part, final)` → `os.Remove(part)` → `fsync(parentDir)`. Atomic; `EEXIST` fails cleanly without touching existing files.
     2. **`rename_noreplace`:** Linux `unix.Renameat2(AT_FDCWD, part, AT_FDCWD, final, unix.RENAME_NOREPLACE)` or macOS `renameatx_np(..., RENAME_EXCL)`.
     3. **`reserve_rename` (probed fallback for SMB/exFAT where hardlink returns `ENOTSUP`):** `O_CREAT|O_EXCL` reservation at `final` before copy, then rename verified `part` over own reservation; recorded explicitly in receipt.
     4. If none succeed during destination probe → fail loudly (`ErrPublicationUnsupported`), never fall back to clobbering `os.Rename`.
   - Preserve source `mtime` (`os.Chtimes`); record `mtime_preserved: bool` honestly in receipt.

#### 4.2.2 Adaptive Concurrency Scheduler (`scheduler.go`)

A single static worker count is wrong for every drive type:
- **Removable flash / spinning HDD (`ROTA=1` or USB card reader):** `1` concurrent reader for large files (`>= 32 MiB`) to maintain pure sequential reads; `4` workers for tiny sidecars (`< 1 MiB`).
- **NVMe / APFS / 10GbE Ethernet:** `2–4` concurrent large streams (enough to fill 10GbE TCP window & NVMe queue depth) and `8–16` concurrent small-file workers to mask `open`/`fsync`/`link` syscall latency.
- Detect storage class automatically via `internal/identity` (`ROTA` + `TRAN` on Linux, `diskutil` Media/Protocol on macOS, or peer link speed).

#### 4.2.3 Crash-Window Reconciliation & Path Reservations (`runner.go`, `reservations.go`)

Carry over the exact 3-window crash model from `transfer_runner.py`:
- **Cross-job path reservations:** Before starting a transfer, reserve every `(destination_id, rel_dest_path)` in `path_reservations`. A concurrent job targeting any overlapping path halts cleanly as `needs_attention` naming the conflicting job.
- **On daemon startup:** Any transfer left in `running`/`verifying` becomes `needs_attention` (resumable).
- **On `transfer.resume`:**
  1. Re-validate source manifest & destination identity bindings.
  2. For each item:
     - *Window 1 (temp written, unpublished):* For local files, discard *this job's* recorded `.ferry-part` and restart the file. For network pulls, keep job-owned `.ferry-part`, hash its existing prefix up to `offset`, verify prefix matches source (or restart from 0 if mismatch), and resume via `Range: bytes=offset-`. Never touch foreign `.ferry-part` files.
     - *Window 2 (published, DB row not committed):* Hash `finalPath` with physical readback; if it matches expected hash + provenance, commit as verified; never re-copy over it.
     - *Window 3 (items committed, receipt file not exported):* Re-export portable JSON receipt without re-copying a single byte.

#### 4.2.4 Honest Dual-Phase ETA & Live Telemetry (`telemetry.go`)

File transfer ETAs in most apps lie wildly because they ignore the verification phase and average over the entire job. Ferry models **two independent phases per destination**:
- Track `emaWriteBPS` and `emaVerifyBPS` (Exponential Moving Average, `α = 0.25`, 1-second buckets; seeded from storage/link probe until first 2s of real data).
- For each destination target:
  ```text
  eta_seconds = (remaining_write_bytes / emaWriteBPS) + (remaining_verify_bytes / emaVerifyBPS)
  ```
- Emit a compact `transfer.progress` event every **150 ms** (coalesced—never spamming the socket per chunk) containing:
  - Overall fraction, bytes written, bytes verified, total bytes, honest ETA seconds.
  - Per-destination breakdown: `target_id`, `label`, `peer_id`, `transport_badge` (e.g. `"10GbE Direct"`, `"USB 3.2 NVMe"`, `"Tailscale Direct"`), `current_file`, `phase` (`"hashing"|"streaming"|"verifying"|"publishing"`), `write_bps`, `verify_bps`, `eta_seconds`, `done_files`, `failed_files`.
  - `actor` (`"human"`, `"agent:claude"`, `"agent:codex"`).

---

### 4.3 Smartest-Path Peer-to-Peer Transport (`internal/p2p/`)

Installing `ferry` on two machines—whether connected directly by a 10GbE cable, through a studio switch, on Wi-Fi, or across Tailscale—should **just work** with zero cloud accounts and zero third-party relays.

#### 4.3.1 Zero-CA Cryptographic Identity & Mutual TLS 1.3 (`identity.go`, `pairing.go`)

- On first run, `ferry daemon` creates `~/.ferry/identity.key` (`0600`, Ed25519 private key) and a self-signed TLS 1.3 certificate embedding that public key.
- **Peer ID:** Base32/hex fingerprint of `SHA-256(ed25519_pubkey)`, formatted readably (e.g. `fry_9a4f2c1b8e7d`).
- **One-command Pairing:**
  - Machine B runs `ferry peer pair` (or clicks "Add Machine" in SwiftUI): starts a 5-minute pairing window, generates a single-use 128-bit random pairing secret, and prints a copyable code (`ferry://pair/<peer_id>?code=<secret>&addr=<best_addrs>`).
  - Machine A runs `ferry peer pair <uri-or-code>`: connects over TLS, proves possession of `<secret>` via HMAC over both TLS exporter channel-bindings (`ConnectionState.ExportKeyingMaterial`) so MITM is cryptographically impossible, and both sides pin each other's Ed25519 public key in `peers`.
- **Every subsequent connection uses Mutual TLS 1.3 (`tls.RequireAnyClientCert` + custom `VerifyPeerCertificate`):**
  - Both server and client reject any certificate whose Ed25519 public key is not in the pinned `peers` table.
  - Zero bearer tokens floating over plain HTTP; everything is encrypted in transit with forward secrecy.

#### 4.3.2 Per-Transfer Scoped Capability Grants (`grant.go`)

This closes the major security gap documented in `MACHINE-TO-MACHINE-SEND.md`:
- Every peer configures explicit **Allowed Roots** in `ferry.toml` (e.g., `/Volumes/Shuttle` `rw`, `/Volumes/CameraCards` `ro`, `/mnt/studio-nas` `rw`). Requests touching anything outside an allowed root are rejected immediately (`403 Forbidden`).
- Even between paired peers, a remote peer **cannot fetch arbitrary files by hash or path**.
- When an approved transfer begins, the source peer generates a **Transfer Grant**:
  ```json
  {
    "grant_id": "grt_...",
    "transfer_id": "xfr_...",
    "issuer_peer": "fry_source...",
    "grantee_peer": "fry_dest...",
    "expires_at": "2026-11-22T25:30:00Z",
    "items": { "item_001": "<sha256_or_size>", ... }
  }
  ```
  Signed with the source peer's Ed25519 private key.
- Every `GET /v1/transfers/{transfer_id}/items/{item_id}` request checks:
  1. `mTLS authenticated peer == grant.grantee_peer`
  2. Valid Ed25519 signature by local key & `now < grant.expires_at`
  3. `item_id` is in `grant.items` and maps to a real path inside an allowed root (re-checked with `lstat` containment)
  4. Client sets `CheckRedirect` to **always refuse redirects** (grant never leaks).

#### 4.3.3 Multi-Interface Discovery & "Path Radar" (`discovery.go`, `pathradar.go`)

A Mac Studio + Linux server often have **multiple simultaneous paths** (direct 10GbE Thunderbolt-to-Ethernet link, 1GbE house switch, Wi-Fi, and Tailscale). Ferry automatically picks the fastest one:

1. **Endpoint Discovery:**
   - **mDNS/Bonjour (`_ferry._tcp`):** Advertises Peer ID, listening port, and all non-loopback interface IPs + link speeds.
   - **Interface Classification:**
     - Linux: reads `/sys/class/net/<iface>/speed` (e.g., `10000`, `2500`, `1000`) and `/sys/class/net/<iface>/wireless` to distinguish 10GbE / 2.5GbE / 1GbE wired vs Wi-Fi vs Tailscale (`tailscale0` / `100.64.0.0/10`).
     - macOS: inspects `net.Interfaces()` + `sysctl`/interface media speed (Thunderbolt Bridge / 10GbE / 1GbE / Wi-Fi / `utun` Tailscale).
   - **Tailscale / Static Peers:** Reads `tailscale status --json` (optional, non-fatal if absent) and static addresses from `peers` table.
2. **Path Tiering & Active Probing:**
   - Endpoints are grouped into tiers:
     - **Tier 1:** Direct Link / Thunderbolt Bridge / 10GbE+ Ethernet
     - **Tier 2:** Wired LAN Ethernet (1GbE / 2.5GbE)
     - **Tier 3:** Local Wi-Fi
     - **Tier 4:** Tailscale / WireGuard Direct
     - **Tier 5:** Configured WAN IP/hostname
   - Before starting a transfer (and every 30s in background while peers are online), **Path Radar** dials the top candidates in parallel (500 ms timeout) and hits `GET /v1/probe?bytes=262144` (256 KiB random buffer) to verify reachability, RTT, and effective link tier.
   - Selects the highest-throughput reachable path and tunes TCP buffers (`SO_SNDBUF`/`SO_RCVBUF` up to 4–8 MiB for 10GbE high-BDP links).
   - **Mid-transfer failover:** If the active interface drops (e.g., Ethernet cable bumped), Path Radar immediately fails over to the next healthy path (e.g., Tailscale) and resumes the active `.ferry-part` from its exact verified byte offset—updating the live `transport_badge` in real time.

#### 4.3.4 P2P Wire Protocol (mTLS + HTTP/2)

| Endpoint | Direction | Purpose |
|---|---|---|
| `GET /v1/probe` | Any paired peer | Health, Peer ID, capabilities, optional 256 KiB speed/RTT probe payload |
| `GET /v1/roots` | Any paired peer | List exported allowed roots + free space + volume identity |
| `POST /v1/inventory` | Planner → Source peer | Scan allowed source folder (returns paginated entries, camera hints, scan errors, manifest hash) |
| `POST /v1/preflight` | Planner → Dest peer | Validate destination path, volume identity, free space, publish strategy, existing collisions |
| `POST /v1/grants` | Coordinator → Source peer | Create signed ephemeral Transfer Grant for an approved plan |
| `GET /v1/transfers/{id}/items/{item}` | Dest peer → Source peer | Stream file bytes directly (`Range: bytes=N-` resume, `X-Ferry-Grant`, `X-Ferry-SHA256` trailer) |
| `POST /v1/transfers/{id}/land` | Coordinator → Dest peer | Execute landing job (pulls directly from source peer's best path; streams live progress back over chunked NDJSON/SSE) |
| `POST /v1/verify` | Verifier → Dest peer | Re-hash landed files on remote peer with physical disk readback against receipt |

**Notice what happens for fan-out (Local SD Card → Local SSD + Remote Linux Server):**
- Local daemon reads the SD card **once**, hashes it, writes to local SSD, AND either pushes chunks over an HTTP/2 `PUT /v1/transfers/{id}/stream/{item}` directly to the Linux server OR lets the Linux server pull while local SSD writes—choosing **push** when the source is a removable card/laptop (so the card is read literally once!) and **pull** when coordinating two remote peers A → B (so the coordinator carries zero bytes!).
- Both push and pull obey the exact same mTLS + scoped grant + `.ferry-part` + physical readback + atomic no-replace publication rules.

---

### 4.4 Organization Presets, Camera Detection & Visual Tree API (`internal/preset`, `internal/metadata`, `internal/planner/tree.go`)

#### 4.4.1 Preserved Spec Semantics (Zero Regressions)
All rules from [`DESTINATION-PRESETS-PRODUCTION-SPEC.md`](DESTINATION-PRESETS-PRODUCTION-SPEC.md) carry over verbatim:
- **Immutable revisions:** Editing a preset appends revision `N+1` with a SHA-256 content hash. Destinations stay pinned to their saved revision until explicitly updated.
- **Evaluation order:**
  1. Explicit **exclusion rules** (recorded with rule ID + non-empty reason, never silently dropped).
  2. **Keep-together folder groups** (outermost group wins; evaluated once at group root; group-root collision shifts the whole group root deterministically rather than suffixing individual members).
  3. **Ordered file rules** (first match wins; AND across conditions within a rule, OR across values within a condition; case-insensitive `/` globs where `*` spans separators).
  4. **Fallback template** (unknown files always routed visibly, never dropped).
- **Deterministic conflict handling:** `keep_both` (`name (2).ext`, `name (3).ext`, ...), `skip_identical` (full checksum + provenance only, never size/mtime), `needs_review` (blocks approval).
- **Portable JSON import/export:** Presets contain relative rules only—never absolute paths, drive UUIDs, peer IDs, or credentials.

#### 4.4.2 New Production & Renaming Capabilities

To support real daily creative workflows (e.g., *"identify camera as ACAM, rename clips to `ACAM_<name>`, file under `ACAM/<date>/`"*):

1. **Pure-Go Camera & Card Fingerprinting (`internal/metadata/camera.go`):**
   - Inspects directory structure & file headers with zero FFmpeg dependency:
     - **Sony:** `PRIVATE/M4ROOT/CLIP`, `XDROOT`, `MEDIAPRO.XML`, `.XML` sidecars (`Device/@manufacturer`, `@modelName`, `@serialNo`).
     - **RED:** `*.RDC/*.R3D` (camera ID & reel from filename/header).
     - **ARRI:** `*.ari` / `.mxf` reel & camera index.
     - **Blackmagic:** `.braw` QuickTime atom metadata.
     - **Canon:** `DCIM`, `CONTENTS/CLIPS001`, `.CRM`/`.MP4`/`.MXF`.
     - **Panasonic / Fujifilm / DJI / GoPro:** folder & MP4/EXIF model tags.
     - **Audio Recorders (Zoom / Sound Devices):** BWF `.WAV` `bext` + `iXML` chunk (`<TAPE>`, `<SCENE>`, `<TAKE>`).
   - **Persistent Camera Role Memory (`camera_bindings` table):** If a user labels Sony FX3 serial `12345` as `ACAM`, Ferry automatically recognizes that camera on every future card offload and proposes `ACAM` in the plan preview (always overridable before approval).

2. **Pure-Go Fast Media Metadata Reader (`internal/metadata/probe.go`):**
   - Reads only container headers (typically first/last 64 KiB, **never** decodes frames) in pure Go:
     - **MP4 / MOV / M4V / BRAW:** walks `moov` → `mvhd` (creation time), `udta`/`meta` (QuickTime camera make/model/creation date with timezone), `trak`/`tkhd` (width/height), `tmcd` (start timecode).
     - **JPEG / TIFF / DNG / RAW / HEIC:** EXIF `DateTimeOriginal`, `Make`, `Model`.
     - **WAV (BWF):** `bext` origination date/time + `iXML`.
   - Processes 1,000 clips in <150 ms with zero external binaries. Optional `ffprobe` enrichment can be enabled if installed.

3. **Path & Filename Renaming Templates (`internal/preset/template.go`):**
   - Templates control **both folder hierarchy and filename**:
     - Example: `{camera_role}/{capture_date}/{camera_role}_{stem}{ext}`
     - Input: `PRIVATE/M4ROOT/CLIP/C0042.MP4` (Sony FX3 tagged `ACAM`, shot `2026-11-22`)
     - Output: `ACAM/2026-11-22/ACAM_C0042.MP4`
   - **Supported tokens:**
     - Structure/Name: `{source_label}`, `{relative_dir}`, `{filename}`, `{stem}`, `{ext}`, `{category}`, `{seq:04d}` (deterministic sorted sequence per folder/rule)
     - Camera/Media: `{camera_role}`, `{camera_model}`, `{reel}`, `{resolution_bucket}` (`1080p`, `4K`, `6K`, `8K`)
     - Dates (explicitly distinguished!):
       - `{capture_date}` (`YYYY-MM-DD`), `{capture_year}`, `{capture_month}` — from embedded media metadata only.
       - `{mtime_date}`, `{mtime_year}`, `{mtime_month}` — filesystem modification time in UTC.
   - **Honest Missing-Metadata Rule:** If a template uses `{capture_date}` or `{camera_role}` and a file has no embedded capture date or camera role, Ferry **never** silently substitutes "today" or an empty string that creates `_C0042.MP4`. Instead, the rule can declare an explicit fallback token (e.g., `{capture_date|mtime_date}`—opt-in!) or route to the preset's fallback with a clear review warning.

4. **Built-in Starter Presets:**
   - `preserve-source-structure`: `Sources/{source_label}/{relative_dir}/{filename}` (safe default).
   - `sort-by-category`: Category folders preserving `{source_label}/{relative_dir}/{filename}`.
   - `camera-card-conform`: `{camera_role}/{capture_date}/{camera_role}_{filename}` with automatic sidecar companion preservation (e.g. Sony `.XML` stays with its clip under the same `{camera_role}` folder).

#### 4.4.3 Before → After Visual Tree API (`internal/planner/tree.go`)

Deep folder trees are unreadable as flat 50,000-row tables. The planner builds a fast server-side **Tree Diff Projection** queryable lazily over IPC/MCP:

```json
// IPC: plan.tree { "plan_id": "pln_...", "destination_id": "dst_1", "parent_path": "", "depth": 2, "limit": 200 }
{
  "root": {
    "path": "",
    "file_count": 148,
    "total_bytes": 91268055040,
    "conflict_count": 0,
    "renamed_count": 142,
    "excluded_count": 6,
    "children": [
      {
        "name": "ACAM",
        "path": "ACAM",
        "kind": "dir",
        "file_count": 84,
        "total_bytes": 54120000000,
        "matched_rule": "camera-video-conform",
        "children": [
          {
            "name": "2026-11-22",
            "path": "ACAM/2026-11-22",
            "kind": "dir",
            "file_count": 84,
            "sample_transforms": [
              { "before": "PRIVATE/M4ROOT/CLIP/C0001.MP4", "after": "ACAM/2026-11-22/ACAM_C0001.MP4", "action": "copy" }
            ]
          }
        ]
      }
    ]
  }
}
```
This lets the SwiftUI app render an instant, collapsible visual tree showing **exactly how messy source folders transform into clean destination folders** before a single byte moves.

---

### 4.5 Storage Identity & Hot-Plug Radar (`internal/identity/`)

Port the evidence hierarchy from `volume_identity.py` into pure Go:
- **Strong:** Filesystem/Volume UUID (`diskutil info -plist` on macOS, `lsblk` / `/dev/disk/by-uuid` on Linux) or sanitized `server/share` for SMB/NFS (stripping any `user:pass@` credentials before storage).
- **Medium:** Hardware disk/media UUID.
- **Weak:** Mount path, volume label, `st_dev` alone — **never** authorizes automatic rebinding.
- **Time-bounded probes:** Every platform probe runs with a strict `5s` context timeout so a hung SMB share never stalls the daemon or UI.
- **Hot-plug watcher:** Detects mounted/ejected drives on macOS and Linux, runs camera card fingerprinting in <100ms, matches saved destinations by UUID, and emits `drive.mounted` / `drive.unmounted` events over the Unix socket so the SwiftUI menu bar can surface *"Sony FX3 Card detected · 68 GB · Ready to offload"*.

---

### 4.6 Durable SQLite Ledger & Portable Receipts (`internal/ledger`, `internal/receipt`)

#### SQLite Setup
- Location: `~/Library/Application Support/ferry/ferry.db` (macOS), `~/.local/share/ferry/ferry.db` (Linux), overridable via `FERRY_DB` or `--db`.
- Pragmas: `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`, `synchronous=NORMAL`.
- All write transactions go through a single serialized write lock in `ledger.DB` (readers run concurrently via WAL) — completely eliminating intermittent SQLite lock errors.

#### Core Schema (`0001_init.sql`)
- `peers (id, name, pubkey_b64, addresses_json, last_seen, last_transport, paired_at, revoked)`
- `saved_destinations (id, name, peer_id, location_kind, root_path, rel_subpath, identity_kind, identity_value, identity_confidence, pinned_preset_id, pinned_preset_rev, default_conflict, checksum_algo, reserve_bytes, archived, created_at, updated_at)`
- `camera_bindings (id, make, model, serial, default_role, updated_at)`
- `presets (id, name, archived, created_at, updated_at)`
- `preset_revisions (id, preset_id, revision, name, description, rules_json, groups_json, exclusions_json, fallback_template, conflict_policy, content_hash, created_at)`
- `inventories (id, peer_id, source_path, source_label, status, file_count, total_bytes, error_count, camera_hint_json, manifest_hash, created_at, completed_at)`
- `inventory_entries (id, inventory_id, rel_path, entry_type, size, mtime_ns, scan_status, scan_error, metadata_json)`
- `plans (id, fingerprint, status, actor, preset_id, preset_rev, preset_hash, camera_roles_json, created_at, approved_at, invalidated_reason)`
- `plan_destinations (id, plan_id, destination_id, peer_id, root_path, identity_snapshot_json, publish_strategy, free_bytes, required_bytes, reserve_bytes)`
- `plan_entries (id, plan_id, plan_dest_id, inventory_id, inventory_entry_id, source_path, dest_rel_path, action, matched_rule, group_id, conflict_kind, exclusion_reason, excluded_by_user, size, mtime_ns)`
- `transfers (id, plan_id, status, actor, started_at, ended_at, canceled, error_summary)`
- `transfer_targets (id, transfer_id, plan_dest_id, peer_id, root_path, transport_kind, peer_addr, status, bytes_written, bytes_verified, files_done, files_failed, ema_write_bps, ema_verify_bps, started_at, ended_at)`
- `transfer_items (id, transfer_target_id, plan_entry_id, status, part_path, final_path, bytes_copied, source_hash, dest_hash, checksum_algo, readback_mode, publish_strategy, mtime_preserved, error, updated_at)`
- `path_reservations (dest_key, dest_rel_path, transfer_id, created_at, PRIMARY KEY (dest_key, dest_rel_path))`
- `receipts (id, transfer_id, plan_id, status, actor, digest, body_json, exported_path, export_error, created_at)`
- `verifications (id, receipt_id, transfer_id, peer_id, folder, checked, missing, modified, added, unreadable, clean, readback_mode, detail_json, created_at)`

#### Portable Receipt (`ferry-receipt-v2.json`)
Written atomically to DB first (in the same transaction that closes the transfer and releases reservations), then exported as JSON beside the destination (or in `~/.ferry/receipts/`). Contains:
- Receipt ID, schema version (`2`), timestamp, actor (`human` / `agent:<name>`), plan fingerprint, preset name/revision/hash.
- For every destination target: peer ID, volume identity, transport link used (`10GbE Direct`, `USB 3.2 NVMe`, `Tailscale Direct`), publish strategy, disk readback mode, duration, sustained throughput.
- For every file: source path, destination path, action (`copied` | `skipped_identical` | `excluded` | `failed`), byte size, `source_hash`, `dest_hash`, `checksum_algo`, `matched_rule`, `camera_role`.
- Tamper-evident `receipt_digest` (`SHA-256` over sorted `[dest_target, dest_rel, dest_hash]`).

---

### 4.7 Local Daemon IPC & SwiftUI Contract (`internal/daemon`, `internal/ipc`)

The SwiftUI macOS app (and CLI / MCP) never touches SQLite or disks directly. Everything talks to `~/.ferry/ferry.sock` (mode `0600`, user-only Unix domain socket) using newline-delimited JSON-RPC 2.0 + server-push events.

#### RPC Methods (Summary)
- **System & Radar:** `daemon.status`, `doctor.run`, `drive.list`, `peer.list`, `peer.pair.start`, `peer.pair.complete`, `peer.probe`
- **Destinations & Presets:** `dest.list`, `dest.save`, `dest.resolve`, `dest.rebind`, `dest.archive`, `preset.list`, `preset.get`, `preset.save`, `preset.import`, `preset.export`, `camera.bind`
- **Unified Transfer Lifecycle:** `plan.create`, `plan.get`, `plan.tree`, `plan.entries`, `plan.resolve`, `plan.approve`, `transfer.start`, `transfer.list`, `transfer.get`, `transfer.cancel`, `transfer.resume`
- **Proof & Verification:** `receipt.list`, `receipt.get`, `receipt.export`, `verify.run`
- **Live Push Stream:** `events.subscribe` (filterable by topic)

#### Live Push Events (Consumed by SwiftUI Menu Bar & Window)
- `transfer.progress` (every 150ms while active): `transfer_id`, `actor`, `overall_fraction`, `eta_seconds`, per-target `{label, peer_id, transport_badge, phase, current_file, write_bps, verify_bps, eta_seconds, done, total, failed}`.
- `transfer.state`: `queued` → `running` → `verifying` → `completed` | `partial` | `needs_attention` | `failed` | `canceled`.
- `drive.mounted` / `drive.unmounted`: drive info, volume UUID, matched saved destinations, detected camera card summary.
- `peer.changed`: online/offline, best active transport tier (`10GbE Direct`, `LAN 1GbE`, `Tailscale Direct`), measured RTT/speed.

---

### 4.8 First-Class Agent Interface: MCP Server & Policy (`internal/mcp`, `internal/policy`)

Running `ferry mcp` exposes Ferry as a standard MCP stdio server for Claude Code, Codex, Antigravity, Cursor, or any MCP client. Because `ferry mcp` talks to `~/.ferry/ferry.sock`, **every transfer an agent triggers streams live into the user's SwiftUI menu bar and main window with actor attribution** (e.g., `"Triggered by Claude"`).

#### MCP Tool Catalog

| Tool | Effect | Description |
|---|---|---|
| `ferry_status` | read | Daemon health, active transfers, connected drives, online peers & best links |
| `ferry_drives` | read | Mounted drives/volumes with UUIDs, free space, and camera-card detection |
| `ferry_peers` | read | Paired machines, their exported roots, and Path Radar link speeds |
| `ferry_destinations` | read | Saved destinations with live availability & pinned presets |
| `ferry_presets` | read | Available organization/renaming presets and their rules |
| `ferry_plan` | plan | Build a concrete multi-destination or cross-machine plan (supports preset, camera roles) |
| `ferry_plan_tree` | read | Inspect the Before → After destination folder tree & conflicts before approving |
| `ferry_transfer_start` | execute | Approve (if permitted by policy) & execute a plan by exact fingerprint; returns `transfer_id` |
| `ferry_transfer_status` | read | Live progress, throughput, active transport path, ETA, and per-target status |
| `ferry_transfer_cancel` | write | Safely cancel a running transfer (preserving verified files & partial receipt) |
| `ferry_receipt` | read | Retrieve full cryptographic receipt (hashes, destinations, exclusions, digest) |
| `ferry_verify` | verify | Re-hash landed files with physical disk readback against a receipt (local or remote peer) |

#### Agent Safety Policy (`~/.ferry/ferry.toml`)

```toml
[daemon]
listen_port = 9410
checksum_algo = "sha256" # "sha256" | "blake3"

[[roots]]
path = "/Volumes/Shuttle"
access = "rw"

[[roots]]
path = "/Volumes/CameraCards"
access = "ro"

[agent_policy]
# Agents can NEVER delete sources or overwrite destinations regardless of config.
auto_approve_saved_destinations = true
auto_approve_max_bytes = 536870912000 # 500 GiB; above this, wait for human approval in SwiftUI/CLI
allow_unsaved_destinations = false    # Require human approval or saved dest
```

Unknown keys in `ferry.toml` fail daemon startup naming the offending key.

---

### 4.9 Park Thin Adapter (Phase 5)

Once the Go daemon is proven, `park_plugins/ferry/` shrinks to a minimal adapter:
- `ferry.plan` → calls Go daemon `plan.create`
- `ferry.offload` / `ferry.send` → calls Go daemon `transfer.start` (single front door!)
- `ferry.transfer.get/list` → reads Go daemon `transfer.get/list`
- `ferry.receipt.get/list` → reads Go daemon `receipt.get/list`
- `ferry.verify` → calls Go daemon `verify.run`

Park keeps its project filing (`ferry.receipt.file`) and workspace UI intact, while all heavy lifting, P2P networking, and safety invariants live in **one** Go codebase.

---

## 5. Phased Implementation Plan & Exit Gates

Each phase must pass its automated verification suite (`go test -race ./...`, `golangci-lint run`, and specific failure-injection tests) before advancing to the next phase.

### Phase 0 — Baseline, Safety Tag & Go Scaffold
- Create local tag `legacy-python-v0.3` on current HEAD (do **not** delete Python/Electron files yet; do **not** push).
- Initialize `go.mod` (`Go 1.24+`), package skeleton under `cmd/ferry` and `internal/`, strict `internal/config`, `Makefile`, and `.github/workflows/ci.yml` (Go checks).
- **Exit gate:** `go build ./...`, `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/ferry`, `GOOS=linux GOARCH=amd64`, and `GOOS=darwin GOARCH=arm64` all produce static binaries cleanly.

### Phase 1 — Core Verified Copy Engine, Fan-Out, Storage Identity & SQLite Ledger
- Build `internal/ledger` (SQLite WAL + migrations + serialized write tx), `internal/storage` (containment `lstat`, `PublishStrategy` probe for hardlink / `rename_noreplace` / `reserve_rename`, `F_NOCACHE` & `FADV_DONTNEED` physical readback), `internal/identity` (macOS + Linux volume UUID + sanitized share identity + time-bounded probes), `internal/engine` (2 MiB `sync.Pool`, single-read N-destination fan-out, adaptive concurrency, dual-phase EMA ETA, cross-job path reservations, crash-window reconciliation), `internal/receipt`, and `internal/verify`.
- **Mandatory failure-injection tests:**
  - `T1.1` Source file mutated mid-copy → fails cleanly, zero partial published.
  - `T1.2` Existing file created at destination after plan (`EEXIST` race) → refuses overwrite, original intact.
  - `T1.3` Symlink at leaf or any ancestor directory → blocked via `lstat`, zero writes.
  - `T1.4` Destination mount `st_dev` changes (simulated unmounted share with surviving local mount dir) → halts before publishing into backing disk.
  - `T1.5` Single-read fan-out to 3 destinations simultaneously where destination #2 injects write error / ENOSPC → destinations #1 & #3 complete and verify cleanly; #2 fails accurately; receipt reflects exact per-target truth.
  - `T1.6` Cancellation mid-chunk on a 100 MiB synthetic file → aborts within <200ms, cleans job-owned `.ferry-part`, preserves prior verified files.
  - `T1.7` All 3 crash windows (unpublished part, published uncommitted, committed unexported receipt) recover cleanly on `resume` without re-copying verified files.
  - `T1.8` Verify `readback_mode` is non-buffered (`f_nocache` on macOS, `fadvise_dontneed` on Linux) and `ferry verify` catches 1-byte bitflip, missing file, and added file with exit codes `0/1/2/3/4`.

### Phase 2 — Organization Presets, Camera/Metadata Engine & Visual Tree API
- Build `internal/metadata` (pure-Go MP4/MOV/EXIF/BWF reader + camera card fingerprinter + `camera_bindings`), `internal/preset` (immutable revisions, rule engine, keep-together groups, exclusions, renaming templates, portable JSON import/export), and `internal/planner` (unlimited full inventory scanner, conflict matrix, capacity estimator, deterministic plan fingerprint, lazy Before → After `plan.tree` trie).
- **Mandatory acceptance tests (porting `A01–A16` & `E01–E16`):**
  - `T2.1` 10,001 mixed files planned & queried via paginated `plan.tree` and `plan.entries` with zero truncation.
  - `T2.2` Camera card fixture (Sony `PRIVATE/M4ROOT/CLIP` + `.XML` sidecars) with `ACAM` binding & template `{camera_role}/{capture_date}/{camera_role}_{filename}` → deterministic conformed output + verified `plan.tree` diff.
  - `T2.3` Missing capture date on a file under `{capture_date}` template → never fabricates today; routes to fallback/review with explicit reason.
  - `T2.4` Keep-together group collision → whole group shifts to suffixed root; internal relative paths untouched.
  - `T2.5` Unicode NFC/NFD collision, case-only collision, same-size/different-bytes conflict (`skip_identical` refused without full checksum + provenance), duplicate destination across two sources → all caught deterministically.
  - `T2.6` Editing preset, destination, binding, or camera role invalidates pending approval in same transaction; stale fingerprint rejected at `transfer.start`.

### Phase 3 — Standalone P2P mTLS, Path Radar & Scoped Transfer Grants
- Build `internal/p2p` (Ed25519 identity, pairing with channel-bound secret, strict mTLS 1.3 peer pinning, signed per-transfer capability grants, mDNS `_ferry._tcp` + interface speed + Tailscale discovery, Path Radar parallel probing + mid-transfer failover, HTTP/2 streaming push/pull with `Range` resume, remote inventory/preflight/verify endpoints).
- **Mandatory security & network tests:**
  - `T3.1` Two local daemons pair, discover each other, and complete a verified folder send + fan-out (local SSD + remote peer simultaneously) + remote `ferry verify`.
  - `T3.2` Unpaired peer TLS handshake rejected; paired peer with missing/expired/wrong-peer/wrong-item grant rejected (`403`); path traversal outside configured `[[roots]]` rejected (`403`).
  - `T3.3` Redirect injection test → client refuses redirect, grant never leaves original peer.
  - `T3.4` Mid-file TCP kill at 50% of a 64 MiB file → resumes from exact verified `.ferry-part` byte offset and verifies final SHA-256.
  - `T3.5` Multi-interface Path Radar test (simulated fast Tier-1 endpoint + slow Tier-4 endpoint) → picks Tier-1; when Tier-1 drops mid-transfer, fails over to Tier-4 and completes cleanly.
  - `T3.6` Real two-machine proof (macOS ↔ Linux over Ethernet/Tailscale, synthetic files only) once D approves deployment.

### Phase 4 — Daemon Unix Socket IPC, Live Telemetry, CLI & MCP Server
- Build `internal/daemon`, `internal/ipc`, `internal/policy`, `internal/mcp`, and all `cmd/ferry` subcommands (`daemon`, `mcp`, `plan`, `approve`, `send`, `status`, `cancel`, `resume`, `verify`, `peer`, `drive`, `dest`, `preset`, `doctor`).
- **Mandatory tests:**
  - `T4.1` Unix socket permissions verified `0600`; concurrent CLI + MCP + event subscriber clients operate cleanly under `go test -race`.
  - `T4.2` MCP end-to-end test: simulated agent calls `ferry_drives` → `ferry_peers` → `ferry_presets` → `ferry_plan` → `ferry_plan_tree` → `ferry_transfer_start` → `ferry_transfer_status` → `ferry_receipt` → `ferry_verify`; live `transfer.progress` events with `actor: "agent:test"` and honest ETA stream to socket subscriber.
  - `T4.3` Agent policy enforcement: agent attempting unsaved destination or transfer exceeding `auto_approve_max_bytes` pauses in `awaiting_approval` until human approves; agent has zero tool surface for source deletion or overwrite.

### Phase 5 — Headless Linux & macOS Packaging, Park Adapter & Legacy Retirement
- Add `packaging/systemd/ferryd.service`, `packaging/launchd/com.lunarpark.ferryd.plist`, `.goreleaser.yaml`, and `ferry doctor` checks.
- Convert `park_plugins/ferry/` into the thin adapter calling the Go daemon and verify `tests/park_plugin/` passes.
- Retire `desktop/`, `src/file_ferry/`, legacy Python tests, and Electron/PyInstaller scripts (preserved in tag `legacy-python-v0.3`).
- Rewrite root `README.md` around the open-source Go binary, Linux headless setup, MCP agent setup, and upcoming SwiftUI macOS app.

### Phase 6 — Security Audit, Fuzzing & Open-Source Release Gates
- Add Go native fuzz targets (`go test -fuzz`) for:
  - Template & path sanitizer (`internal/preset`, `internal/storage/containment.go`)
  - Pure-Go media metadata parsers (`MP4/MOV`, `EXIF`, `WAV/BWF`)
  - P2P grant parser & signature verification
  - Config TOML parser & mount device sanitizer
- Run secret scan (`scripts/secret-scan.sh`), license check on `go.mod` dependencies, and real-hardware soak test (SD card → Thunderbolt NVMe + 10GbE Linux server).
- Ready for SwiftUI app integration and public open-source release.

---

## 6. Reference Notes for the Implementing Agent (Claude)

1. **Read before deleting or changing invariants:** Before implementing Phases 1–2, skim [`src/file_ferry/application/transfer_safety.py`](../src/file_ferry/application/transfer_safety.py), [`src/file_ferry/application/volume_identity.py`](../src/file_ferry/application/volume_identity.py), [`src/file_ferry/application/rules.py`](../src/file_ferry/application/rules.py), and [`docs/DESTINATION-PRESETS-PRODUCTION-SPEC.md`](DESTINATION-PRESETS-PRODUCTION-SPEC.md) to make sure no subtle edge case (e.g., `lstat` ancestor check, NFC/NFD normalization, SMB `reserve_rename` probe, group-root collision suffixing, or empty-directory retention) is lost in translation.
2. **Keep every phase green and self-contained:** Commit per phase on a dedicated local branch (e.g., `feat/go-v2-backend`). Do **not** push to GitHub until explicitly instructed.
3. **Never mock away filesystem or crypto boundaries in core tests:** Use real temp directories, real SQLite WAL files, real Ed25519/mTLS 1.3 handshakes over loopback TCP, and real `go test -race`.
4. **Keep the IPC protocol stable & self-documenting:** Document every JSON-RPC method and push event payload in `docs/IPC-PROTOCOL.md` during Phase 4 so the SwiftUI macOS app can bind directly against it without guesswork.
