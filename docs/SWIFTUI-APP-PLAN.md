# File Ferry v2 — Native SwiftUI macOS App Plan (Unified Park / Slate Design System)

> **Status:** Implementation Spec for Standalone `Ferry.app` (macOS 14+, SwiftUI + AppKit chrome)  
> **Date:** 2026-11-23  
> **Repo:** `/Users/dspury/Lunar-Park/lp-ferry` (branch `feat/go-v2-backend`, baseline `f336a7d`)  
> **Protocol Contract:** [`docs/IPC-PROTOCOL.md`](IPC-PROTOCOL.md) (JSON-RPC 2.0 over `~/.ferry/ferry.sock`, protocol v1)  
> **Visual Reference:** `/Users/dspury/Lunar-Park/park/apple/Sources/ParkKit/Theme.swift`, `/Users/dspury/Lunar-Park/lp-slate/app/Sources/Slate/Theme.swift`, `SlateButtons.swift`, `TitleBar.swift`, `SessionSidebar.swift`

---

## 1. Product Goal

Build a **standalone, native SwiftUI macOS app** (`lp-ferry/app/` → `dist/Ferry.app`) that talks exclusively to the Go `ferry daemon` over Unix-socket JSON-RPC (`~/.ferry/ferry.sock`) and carries the **exact Lunar Park / Slate unified visual language**. Ferry must work 100% standalone (zero runtime dependency on Park or Electron, open-source buildable from `lp-ferry` alone) while feeling like a first-class sibling of `Park.app` and `Slate.app`.

---

## 2. Unified Park / Slate Visual Design System (Mandatory Styling Spec)

Every view MUST follow the Lunar Park studio aesthetic — **calm mineral graphite, smoked translucent glass, warm ivory ink, neutral selection, and one burnished amber reserved strictly for the primary action and brand mark.** No default macOS system-blue buttons, no pure `#000000` backgrounds, no garish neon green status pills, and no literal hex colors scattered in view files.

### 2.1 Exact Color Tokens (`FerryTheme.swift`)
Mirror `ParkKit.Palette` 1:1:
- **Room & Surfaces:**
  - `ground = #202225`, `groundDeep = #15171A`
  - `plate = #27292C`, `plateHover = #303236`, `plateRaised = #3B3E42`, `well = #1D1F22`
- **Hairlines & Rules:**
  - `rule1 = #303337`, `rule2 = #3A3D41`, `rule3 = #464A4F`, `rule4 = #5B6066`
  - `glassFill = white @ 2.5%`, `glassHover = white @ 4.5%`, `glassSelected = white @ 8%`, `glassLine = white @ 5.5%`, `glassEdge = white @ 10%`, `glassCard = white @ 1.8%`, `glassStrong = #2C2F33 @ 94%`
- **Typography Ink (all clear 4.5:1 on ground/plate/well):**
  - `ink1 = #EEEEE8` (warm ivory primary)
  - `ink2 = #CED0CD`
  - `ink3 = #B1B6B9`
  - `ink4 = #9CA3AA`
  - `ghost = #72787E`
- **Accent (Primary Action & Brand Mark ONLY — never selection or generic status):**
  - `accent = #DFAA6E`, `accentHover = #EDBA7E`, `accentSoft = #DFAA6E @ 9%`, `accentLine = #DFAA6E @ 32%`, `onAccent = #241B11`
- **Selection (Strictly Neutral):**
  - `sel = #D7DBDC`, `selSoft = #D7DBDC @ 9%`
- **Status (Shape glyph + word FIRST, color THIRD — must read clearly in greyscale):**
  - `ok = #B7BDC3` (calm smoked silver — verified / clean / online)
  - `run = #80B9EF` (`runSoft = #1A2632`, `runLine = #80B9EF @ 34%` — streaming / verifying)
  - `warn = #E2BC70` (`warnSoft = #28241C`, `warnLine = #E2BC70 @ 40%` — awaiting approval / needs review / partial / offline warning)
  - `bad = #F07B74` (`badSoft = #2E1D1E` — failed / modified / blocked)
  - `idle = #888C93`

### 2.2 Typography & Layout Rhythm
- **Font:** Self-hosted `Inter.woff2` bundled in `Ferry.app/Contents/Resources/Inter.woff2` (registered via `CTFontManagerRegisterFontsForURL`), with clean system fallback if absent in unit tests.
  - Scale: `xs = 12`, `sm = 14`, `base = 15`, `md = 17`, `lg = 22`, `xl = 30`.
  - Section headers: `10.5–11 pt`, `.semibold`, uppercase, `.tracking(0.7)`, `ink4` (or `warn` for Attention/Approvals).
  - ALL numbers (bytes, MB/s, file counts, ETAs, percentages, timestamps) MUST use `.monospacedDigit()`.
  - Paths, SHA-256/BLAKE3 digests, peer IDs, and template tokens use `.system(size: ..., design: .monospaced)`.
- **Spacing Scale (`Space`):** `4, 8, 12, 16, 20, 24, 32, 48`.

### 2.3 Surfaces, Glass, Buttons & Window Chrome
- **Ambient Room (`FerryRoom`):** Mineral graphite `#202225` → `#15171A` vertical depth with subtle overhead soft studio radial glow (`white @ 3.5%` top-center) and subtle noise grain (static when `FERRY_SNAPSHOT=1` or `accessibilityReduceMotion` is on).
- **Glass Panels (`paneGlass(radius: 14–16, glow: Color?)`):** Translucent dark smoke (`black @ 10%`), diagonal specular wash (`white @ 6% → 1% → 2.6%`), directional bevel stroke (`white @ 24% → 6% → 11%`), optional subtle status/amber halo (`strokeBorder` + soft blurred outer glow for active approval cards). Never heavy opaque flat gray boxes.
- **Buttons (`PillButton` matching Slate's `SlateButtons.swift`):**
  - `.primary` — solid burnished amber capsule (`accent`, `onAccent` semibold text) — used for ONE primary action per view (e.g., *Approve & Start Transfer*, *Build Plan*, *Verify on Disk*).
  - `.secondary` — quiet ivory wash (`white @ 8%`, `12%` hover, `ink1` text, `white @ 6%` hairline).
  - `.destructive` — soft coral wash (`bad @ 13%`, `20%` hover, `bad` text) — *Cancel Transfer*, *Revoke Peer*.
  - Optional trailing monospaced keyboard hint (`↩`, `esc`, `⌘R`).
- **Window Chrome:** Dark `.preferredColorScheme(.dark)` full-size-content window (`titlebarAppearsTransparent = true`, `titleVisibility = .hidden`), unified custom titlebar (`WindowDragArea` + `FirstMouseHostingView`), overlay scrollers (`OverlayScrollers` hiding track bars at rest), and a clean `MenuBarExtra` status icon showing active transfer count / pending agent approvals badge.

---

## 3. Architecture & Daemon Transport (`lp-ferry/app/`)

```text
lp-ferry/app/
├── Package.swift                  # Standalone macOS 14+ SwiftPM package (Ferry executable + FerryCore library + FerryTests)
├── Sources/
│   ├── FerryCore/                 # Pure testable client, models, state store & reducers
│   │   ├── DaemonClient.swift     # Multiplexed Unix-socket JSON-RPC 2.0 command link + separate events.subscribe stream + auto-reconnect
│   │   ├── DaemonLifecycle.swift  # Auto-starts bundled `ferry daemon` if socket unavailable; clean health checks
│   │   ├── Models.swift           # Strict Codable structs matching docs/IPC-PROTOCOL.md v1 (zero unknown params!)
│   │   ├── Store.swift            # @MainActor @Observable central store fed by RPC + live Event stream
│   │   ├── Fixtures.swift         # Rich deterministic fixture state (FERRY_FIXTURE=1) for previews, tests & snapshots
│   │   └── Formatters.swift       # Cached byte/rate/ETA/relative-time formatters
│   └── Ferry/                     # SwiftUI macOS App
│       ├── FerryApp.swift         # App entry, WindowGroup, MenuBarExtra, UNUserNotificationCenter for agent approvals
│       ├── Design/                # FerryTheme.swift, Glass.swift, PillButton.swift, StatusGlyph.swift, WindowChrome.swift
│       ├── Shell/                 # MainShell.swift (Left Rail + Primary Canvas + Right Inspector), TitleBar.swift
│       ├── Views/
│       │   ├── TransferWorkspaceView.swift   # Unified local, multi-dest fan-out & P2P send composer + Plan trigger
│       │   ├── PlanTreeView.swift            # Lazy-paged Before → After `plan.tree` browser + conflict/camera/exclusion resolution
│       │   ├── QueueHistoryView.swift        # Active transfers, agent awaiting_approval cards, resumable & completed list
│       │   ├── TransferInspectorView.swift   # Two-phase live Write/Verify meters, Receipt v2 viewer, Verify on Disk button
│       │   ├── MachinesDrivesView.swift      # Live mounted drives (camera card badges) + Path Radar peers + Pair/Probe sheets
│       │   ├── PresetsDestinationsView.swift # Saved destinations (resolve/rebind/archive) + Preset editor/live preview + Camera bindings
│       │   └── DoctorSheet.swift             # `doctor.run` diagnostics & daemon status popover
└── Tests/
    └── FerryCoreTests/            # Swift Testing / XCTest: Codable round-trips, real-daemon JSON-RPC integration, Store event reducers
```

### Daemon Transport Contract
- Connects to `$FERRY_SOCKET` or `~/.ferry/ferry.sock` via POSIX `AF_UNIX` stream sockets (`0600`).
- **Two connections (matching Slate's `DaemonClient`):**
  1. `CommandLink`: kept persistent connection for pipelined JSON-RPC 2.0 requests matched by `id` (never stalls event stream).
  2. `EventStream`: persistent connection calling `events.subscribe` with `{"topics": ["*"]}`, batching incoming events onto `@MainActor` so 150 ms `transfer.progress` ticks are silky-smooth and low-CPU.
- On reconnect, refreshes `daemon.status`, `drive.list`, `peer.list`, `dest.list`, `preset.list`, `transfer.list`, and `receipt.list`, and reconciles any active transfer via `transfer.get`.
- **Never touches SQLite or user files directly** — all mutations go through the daemon as `actor: "human"`.

---

## 4. Complete Feature & Interaction Surface

### 4.1 Left Navigation Rail (Matches Slate's Rail Rhythm)
Resizable (210–300 pt, collapses cleanly to a 52 pt icon strip when narrow):
1. **Transfer** — Compose & review plans (drag-and-drop folders/cards directly onto the window).
2. **Activity** — Live transfers, amber-badged **Agent Approvals** (`awaiting_approval`), resumable `needs_attention` runs, and receipt history.
3. **Machines & Drives** — Local drives (with live `drive.mounted`/`unmounted` updates & camera-card pills) + paired peers with Path Radar transport badges.
4. **Presets & Destinations** — Saved destinations (with live `dest.resolve` health) + preset library & camera bindings.
5. **Footer Status Pill** — Daemon version, peer name, online/offline glyph, click to open **Doctor** (`doctor.run`).

### 4.2 Transfer Workspace & Interactive Before → After `plan.tree`
- **Unified Sources → Destinations Composer:**
  - Add 1..N sources from local Finder picker, drag-and-drop, mounted Drives quick-chips, or a paired Remote Peer path.
  - Add 1..N destinations (local folder, Saved Destination with binding status, or paired Remote Peer) for single copy, simultaneous multi-destination fan-out, or direct P2P push/pull!
  - Preset picker (`preset.list`), Conflict Policy (`keep_both`, `skip_identical`, `needs_review`), Checksum (`sha256`, `blake3`), and optional Camera Role overrides (`A-Cam`, `B-Cam`, etc.).
- **Plan Review & Lazy-Paged `plan.tree`:**
  - Builds plan via `plan.create`, displays plan summary strip (total files, bytes, copy/skip/exclude/needs_review counts, destination free-space safety check, findings list).
  - **Before → After Tree (`plan.tree`):** Interactive disclosure tree paging children on demand (`parent_path` + `next_cursor`), showing folder rollup counts, `renamed` / `conflict` / `excluded` badges, and `before → after` transform previews.
  - **Inline Resolution (`plan.resolve`):** Exclude a file/folder, adjust conflict policy, or assign a camera role — cleanly creates the successor plan and updates the fingerprint.
  - **Exact-Fingerprint Approval:** Primary amber `PillButton` **"Approve & Start"** (disabled whenever `blocking_count > 0` or `needs_review > 0`) calls `plan.approve` + `transfer.start` with the reviewed `fingerprint`.

### 4.3 Live Telemetry, Agent Approvals & Physical Verification (`Activity` + Right Inspector)
- **Agent Approval Gate (`plan.awaiting_approval`):**
  - Prominent amber-glow glass card at the top of Activity (and a subtle floating toast anywhere in the app + macOS `UNUserNotificationCenter` notification when backgrounded).
  - Shows **"Triggered by `<actor>`"**, policy reasons, full expandable `plan.tree` inspection, and human-only **Approve & Start** (`plan.approve`) or **Dismiss**.
- **Two-Phase Live Progress (`transfer.progress`):**
  - Overall fraction + honest ETA + per-target rows showing transport tier badge (`Local SSD`, `Tier 2 · Wired LAN`, `Tier 4 · Tailscale`), separate **Write** and **Readback Verify** progress bars, current file, write/verify MB/s, and **Cancel** / **Resume** buttons.
- **Receipts & Physical Readback Verify (`receipt.get`, `verify.run`):**
  - Right inspector displays `ferry-receipt-v2` metadata: receipt ID, digest (with Copy), readback mode (`F_NOCACHE` / `fadvise_dontneed` / `remote_readback`), actor, exported path (+ Retry Export button if `export_error`), and per-file verification rows.
  - **"Verify on Disk"** button calls `verify.run` and renders a clear clean / missing / modified / added / unreadable result banner with per-target breakdown.

### 4.4 Machines & Drives (`drive.list`, `peer.*`)
- **Drives:** Cards showing volume name, mount path, storage class (`SSD`, `HDD`, `Removable`, `Network`), free space bar, detected camera card badge (`Sony`, `RED`, `ARRI`, etc.), and one-click **"Plan Offload"** pre-filling the Transfer workspace.
- **Peers & Path Radar:** Paired machines showing active route tier badge (`Tier 1 Direct`, `Tier 2 LAN`, `Tier 3 Wi-Fi`, `Tier 4 Tailscale`), last seen, **Probe Routes** (`peer.probe` showing per-endpoint RTT + throughput measurements), **Send to Machine** shortcut, and **Revoke** (`peer.remove`).
- **Pair Machine Sheet:** Two tabs — **Invite** (`peer.pair.start`, displays copyable `ferry://pair/...` URI + generated CoreImage QR code + TTL countdown) and **Join** (`peer.pair.complete` paste box).

### 4.5 Presets, Saved Destinations & Doctor
- **Saved Destinations:** List with live `dest.resolve` state badge (`available`, `offline`, `needs_confirmation`, `ambiguous`, `unwritable`), New/Edit sheet (`dest.save`), **Rebind** (`dest.rebind`), and **Archive** (`dest.archive`).
- **Presets:** Built-in & custom presets (`preset.list`, `preset.get`), rule/group/exclusion viewer + editor (`preset.save`), live **`preset.preview`** dry-run table against sample paths, JSON Import/Export (`preset.import`, `preset.export`), and Camera Binding manager (`camera.bind`).
- **Doctor Popover/Sheet (`doctor.run`):** Every daemon check with shape glyph + detail text + copyable diagnostics.

---

## 5. Packaging, Build & Verification

1. **App Icon & Font:** Generate `assets/AppIcon.icns` from `assets/brand/file-ferry-icon-macOS-v1.png` (using `sips` + `iconutil`) and copy `Inter.woff2` (OFL) into `app/Sources/Ferry/Resources/` (and `Ferry.app/Contents/Resources/`).
2. **`scripts/build-app.sh` + `Makefile` targets (`make app`, `make app-test`):**
   - Builds the Go `ferry` binary (`bin/ferry`) and SwiftPM release binary (`app/.build/release/Ferry`).
   - Assembles `dist/Ferry.app` bundling `Contents/Resources/bin/ferry`, `AppIcon.icns`, `Inter.woff2`, and `Info.plist` (`dev.lunarpark.ferry`, `LSMinimumSystemVersion = 14.0`), signed cleanly (ad-hoc by default, or dev identity when configured).
3. **Verification Gates:**
   - `cd app && swift test` passes (unit tests for `Models`, `Store` reducers, `Formatters`, and real-daemon JSON-RPC integration against a temporary `ferry daemon` in `mktemp -d`).
   - `make app` builds `dist/Ferry.app` cleanly with zero warnings/errors.
   - Launch `FERRY_FIXTURE=1` (and against a live temporary `ferry daemon`) and capture/verify native UI renders for key screens (Transfer + `plan.tree`, Live Activity + Agent Approval, Machines & Drives, Presets & Destinations) confirming unified Park/Slate dark graphite + glass + amber accent styling and zero clipped text/controls.
   - Existing Go checks (`make vet && make race`) remain 100% green.
