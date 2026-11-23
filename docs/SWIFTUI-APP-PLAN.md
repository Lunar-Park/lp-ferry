# File Ferry v2 — Native SwiftUI macOS App Plan (Unified Park / Slate Design System)

> **Status:** Implementation Spec for Standalone `Ferry.app` (macOS 14+, SwiftUI + AppKit chrome)  
> **Date:** 2026-11-23  
> **Repo:** `/Users/dspury/Lunar-Park/lp-ferry` (branch `feat/go-v2-backend`, baseline `f336a7d`)  
> **Protocol Contract:** [`docs/IPC-PROTOCOL.md`](IPC-PROTOCOL.md) (JSON-RPC 2.0 over `~/.ferry/ferry.sock`, protocol v1)  
> **Visual Reference:** the Lunar Park style kit in `park/apple/Sources/ParkKit/` (`RoomShader.swift`, `Glass.swift`, `Theme.swift`) and `park/apple/Sources/ParkNative/` (`Buttons.swift`, `Shell.swift`, `Dashboard.swift`), and `park/docs/design/park-ui/park-desk.png`, `park-tools-v2.png`, `park-inbox.png`

---

## 1. Product Goal

Build a **standalone, native SwiftUI macOS app** (`lp-ferry/app/` → `dist/Ferry.app`) that talks exclusively to the Go `ferry daemon` over Unix-socket JSON-RPC (`~/.ferry/ferry.sock`) and carries the **exact Lunar Park / Slate unified visual language**. Ferry must work 100% standalone (zero runtime dependency on Park or Electron, open-source buildable from `lp-ferry` alone) while feeling like a first-class sibling of `Park.app` and `Slate.app`.

---

## 2. Unified Park / Slate Visual Design System (Mandatory Styling Spec)

Ferry carries a standalone port of the Lunar Park style kit in `app/Sources/Ferry/Design/` (no SwiftPM dependency on `../park`), retuned to the maritime colours measured in the legacy brand guide. The ship mark follows the paths of `assets/brand/ferry-logo-black.svg`, drawn in the app icon's two tones.

### 2.1 Color tokens (`Design/Theme.swift`)

- The room is computed by `FerryRoomShader`: graphite-navy `#162235` → `#131C2B` → `#101722`, washed with steel blue `#75A1C6` at the upper left and a sea-slate swell at the lower right, with a wandering cool light and orbs of wake mist `#9EC5E6`, deep sea slate and dusk. No grain.
- Ink: bone `#F6F5F2` for titles and the mark, body `#EDEBE6`, then three cooler steps, each clearing 4.5:1 on the room and the glass.
- Ferry blue `#75A1C6` is the one accent, for the primary action and the mark; words on accent-tinted glass are ice blue `#C4E0F6`. The current place's disc is mist `#DCE5EA` with a deep ferry-blue `#2D5A83` glyph.
- Status is shape first (a glyph and a word on a faint wash of its colour): green for verified, wake mist for running, amber for approval, violet for attention, coral for failure.

### 2.2 Typography & Layout Rhythm
- **Font:** Self-hosted `Inter.woff2` bundled in `Ferry.app/Contents/Resources/Inter.woff2` (registered via `CTFontManagerRegisterFontsForURL`), with clean system fallback if absent in unit tests.
  - Scale: `xs = 12`, `sm = 14`, `base = 15`, `md = 17`, `lg = 22`, `xl = 30`.
  - Page titles `30` semibold bone in the head band; section titles `22` semibold; body `15`; small labels `12` medium in sentence case.
  - ALL numbers (bytes, MB/s, file counts, ETAs, percentages, timestamps) MUST use `.monospacedDigit()`.
  - Paths, SHA-256/BLAKE3 digests, peer IDs, and template tokens use `.system(size: ..., design: .monospaced)`.
- **Spacing Scale (`Space`):** `4, 8, 12, 16, 20, 24, 32, 48`.

### 2.3 Surfaces, Glass, Buttons & Window Chrome
- **Room and glass (`Design/Glass.swift`, `Design/FerryRoomShader.swift`):** one Metal view draws the living room for the whole window. Every `GlassSurface` reports its frame; the shader bends, frosts (five taps) and colour-fringes the room inside each pane, with a lens rim, and drops a soft shadow outside it. Panes inside a `GlassScroll` stop bending where the scroll view ends. `RoomRenderer.snapshot` draws the same pass into an image, so fixture screenshots show the real refraction. `FERRY_STILL=1` holds the room still.
- **Three places (`Design/Shell.swift`):** the rail is three 40 pt glass discs on the traffic lights' axis (`Page.gutter = 28`), the current one a mist disc, with Ferry Doctor at the foot. ⌘1–3 jump between them. The `Head.band = 64` head carries only the page title (and a calm "Connecting to Ferry…" pill while the daemon is away). There are no corner discs.
  - **Transfer** (home, carries the ship): a live banner only while a copy runs or an agent waits, then one card asking *From* and *To*. Mounted cards and drives, saved destinations, other drives and paired machines are one-click pick rows, with a drop row and Choose menu under each. Options (folder layout, conflict policy, checksum, camera names) stay folded into one summary line. One primary, **Review transfer**, leads to the second stage, **Review & copy** (`PlanReview`: a sentence, the space check, **Start verified copy** or **Approve & start**, then what lands where).
  - **Activity**: All · Active · Approvals · Completed. List rows are titled by route ("A004 → Studio archive", "Claude · Studio archive", resolved by `Routes` from `plan.get` + `plan.entries`); ids appear only as mono metadata in the detail. The detail card holds the Written / Verified on disk bars, Cancel, Resume, and the receipt with **Verify on disk**.
  - **Devices**: *Drives & machines* (Copy from →, Check speeds, Copy to →, Pair machine) and *Destinations & layouts* (saved destinations, folder layouts, editors in sheets).
- **Glass budget:** the room, the rail's discs, the one primary button of a screen, and one or two focal containers (the live banner and the From → To card; the review card; the Activity detail; sheets). Everything else is flat: secondary buttons and menus are soft flat plates, status pills are faint tinted words, transport tags are plain text.
- **Window chrome:** full-size-content window with a transparent title bar; `WindowChrome` centres the traffic lights in the head band. `FirstMouseHostingView`, `WindowDragArea` and overlay scrollers stay. The `MenuBarExtra` shows active transfers and pending approvals.

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
│       ├── Design/                # Theme, FerryRoomShader, Glass, Buttons, Shell, LineIcon, Components, ShipMark, Window
│       ├── MainShell.swift        # Room + three-disc rail + open head band + page
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
The resizable rail collapses to an icon strip at compact widths. Dashboard opens on launch with transfer counters, mounted volumes, paired peers and recent transfers. Transfer, Activity, Machines & Drives, and Presets & Destinations share the same rail. The titlebar daemon pill opens Doctor.

### 4.2 Transfer Workspace & Interactive Before → After `plan.tree`
- **Unified Sources → Destinations Composer:**
  - Add 1..N sources from local Finder picker, drag-and-drop, mounted Drives quick-chips, or a paired Remote Peer path.
  - Add 1..N destinations (local folder, Saved Destination with binding status, or paired Remote Peer) for single copy, simultaneous multi-destination fan-out, or direct P2P push/pull!
  - Preset picker (`preset.list`), Conflict Policy (`keep_both`, `skip_identical`, `needs_review`), Checksum (`sha256`, `blake3`), and optional Camera Role overrides (`A-Cam`, `B-Cam`, etc.).
- **Plan Review & Lazy-Paged `plan.tree`:**
  - Builds plan via `plan.create`, displays plan summary strip (total files, bytes, copy/skip/exclude/needs_review counts, destination free-space safety check, findings list).
  - **Before → After Tree (`plan.tree`):** Interactive disclosure tree paging children on demand (`parent_path` + `next_cursor`), showing folder rollup counts, `renamed` / `conflict` / `excluded` badges, and `before → after` transform previews.
  - **Inline Resolution (`plan.resolve`):** Exclude a file/folder, adjust conflict policy, or assign a camera role — cleanly creates the successor plan and updates the fingerprint.
  - **Exact-Fingerprint Approval:** Primary Ferry-blue `PillButton` **"Approve & Start"** (disabled whenever `blocking_count > 0` or `needs_review > 0`) calls `plan.approve` + `transfer.start` with the reviewed `fingerprint`.

### 4.3 Live Telemetry, Agent Approvals & Physical Verification (`Activity` + Right Inspector)
- **Agent Approval Gate (`plan.awaiting_approval`):**
  - Amber review card in the Activity inspector, a pending count in the rail and a macOS notification when backgrounded.
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
   - Launch `FERRY_FIXTURE=1` (and against a live temporary `ferry daemon`) and capture/verify native UI renders for key screens (Transfer + `plan.tree`, Live Activity + Agent Approval, Machines & Drives, Presets & Destinations) confirming maritime navy glass and Ferry-blue action styling and zero clipped text/controls.
   - Existing Go checks (`make vet && make race`) remain 100% green.
