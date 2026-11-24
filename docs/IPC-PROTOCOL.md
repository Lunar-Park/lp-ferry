# Ferry daemon IPC protocol (v1)

The Ferry daemon (`ferry daemon`) owns every disk, network and database operation. The SwiftUI app, the CLI and the MCP bridge (`ferry mcp`) never touch SQLite or the filesystem directly; they all speak this protocol over one local socket. This document is the contract: field names here are the field names on the wire.

`daemon.status` reports `"protocol": 1`. Additive changes (new methods, new optional fields, new event topics) keep the version; anything that removes or renames a field bumps it.

## Transport

| | |
|---|---|
| Socket | Unix domain socket, default `~/.ferry/ferry.sock` (`[daemon] socket` in `ferry.toml`, or `--socket`) |
| Permissions | socket `0600` inside a `0700` directory; the daemon also checks the peer's uid (`LOCAL_PEERCRED` / `SO_PEERCRED`) and drops other users |
| Framing | one JSON object per line (`\n`-terminated), UTF-8 |
| RPC | JSON-RPC 2.0. Requests may be pipelined; replies can arrive out of order and are matched by `id` |
| Push | server-initiated notifications `{"jsonrpc":"2.0","method":"event","params":Event}` after `events.subscribe` |
| Params | objects; **unknown fields are rejected** (`-32602`), so a typo never silently becomes a default |

```json
→ {"jsonrpc":"2.0","id":7,"method":"plan.get","params":{"plan_id":"pln_3f…"}}
← {"jsonrpc":"2.0","id":7,"result":{"plan_id":"pln_3f…","status":"draft",…}}
← {"jsonrpc":"2.0","method":"event","params":{"topic":"transfer.progress","time":"2026-11-22T10:00:00.15Z","data":{…}}}
```

### Actors

Mutating calls take an optional `actor`: `"human"` (default) or `"agent:<name>"` (`[A-Za-z0-9._-]{1,64}`). The actor is recorded on plans, transfers, receipts and every `transfer.progress` event, so the app can show "Triggered by Claude". `ferry mcp` always sends `agent:<MCP client name>`. Agents are bound by `[agent_policy]` (see `transfer.start`). The socket is per-user and actor strings are self-declared, so the actor is attribution and a policy hook, not an authentication boundary against code running as the same user.

### Errors

| code | meaning |
|---|---|
| -32700 / -32600 / -32601 / -32602 / -32603 | JSON-RPC parse / invalid request / unknown method / invalid params / internal |
| -32000 | the operation ran and failed (message says why) |
| -32001 | stale approval: the plan was edited, invalidated, superseded, already executed, or the fingerprint does not match |
| -32002 | blocked: blocking findings, items needing review, or an agent attempting a human-only decision |
| -32004 | not found |

## Methods

### System

| method | params | result |
|---|---|---|
| `daemon.status` | – | `{version, protocol, name, peer_id?, p2p_addrs?, socket, uptime_seconds, active_transfers: [Progress], awaiting_approval: [plan_id], events_dropped}` |
| `doctor.run` | – | `[{name, ok, detail}]` covering runtime, database (WAL), socket mode, identity key mode, P2P, readback mode, and each root (exists, publish strategy, free space) |
| `events.subscribe` | `{topics?: [string]}` | `{subscribed}`. Topics are exact (`transfer.progress`), prefix (`transfer.*`) or `*`; none means all. Events then arrive as notifications on this connection until it closes. |

### Drives and peers

| method | params | result |
|---|---|---|
| `drive.list` | – | `{drives: [Drive], warnings, degraded}` |
| `peer.list` | – | `[{id, name, pubkey_b64, addresses, last_seen, last_transport, paired_at, revoked, active_path?: Endpoint}]` |
| `peer.pair.start` | `{ttl_seconds?}` (≤ 1800, default 300) | `{code, expires_at, peer_id}`. `code` is `ferry://pair/<peer_id>?code=<secret>&ep=<endpoint>…`, single use. |
| `peer.pair.complete` | `{code}` | the pinned peer |
| `peer.probe` | `{peer_id}` | `{peer_id, active_path?: Endpoint, measurements: [{addr, tier, badge, rtt, bps, error?}], info?, error?}` |
| `peer.remove` | `{peer_id}` | `{revoked: true}`; the key is refused from then on |

`Drive` = `{mount_path, identity?: {kind, value, confidence, provenance}, class: ssd|hdd|removable|network|unknown, writable, free_bytes, saved_destinations?: [id], card?: {vendor, format, confidence, evidence}}`

`Endpoint` = `{addr, tier: 1 direct/10GbE | 2 LAN | 3 Wi-Fi | 4 Tailscale | 5 WAN, badge}`

### Destinations, presets, cameras

| method | params | result |
|---|---|---|
| `dest.list` | `{include_archived?}` | `[SavedDestination + {resolution}]` |
| `dest.save` | `{id?, name, path, preset_id?, preset_rev?, default_conflict?, checksum_algo?, reserve_bytes?, archived?}` | `SavedDestination`. Editing an existing id **invalidates its pending plans** in the same transaction. |
| `dest.resolve` | `{id}` | `SavedDestination + {resolution: {state: available\|offline\|needs_confirmation\|ambiguous\|unwritable, reason, binding?, candidates?}}` |
| `dest.rebind` | `{id, path}` | `SavedDestination` (invalidates pending plans) |
| `dest.archive` | `{id}` | `SavedDestination` |
| `preset.list` | – | `[{id, name, latest_revision, archived, builtin}]` |
| `preset.get` | `{id, revision?}` | `{preset_id, revision, content_hash, created_at, preset: Preset}` |
| `preset.save` | `{id?, preset: Preset}` | new revision (`id` absent: new preset at revision 1). A new revision **invalidates pending plans** built on that preset. |
| `preset.import` | `{json}` | new preset (never overwrites one) |
| `preset.export` | `{id, revision?}` | portable `Preset` JSON |
| `preset.preview` | `{id, revision?, source_label, paths: [rel]}` | `[{rel, is_dir, action, dest_rel, rule, group_id?, group_root?, group_dest?, exclusion_reason?, warning?}]` |
| `camera.bind` | `{make, model, serial, role}` | `{camera_key, role}`; invalidates pending plans that used that camera |

`SavedDestination` = `{id, name, peer_id, location_kind: local_folder|volume_folder|mounted_share_folder, root_path, rel_subpath, identity_kind, identity_value, identity_confidence, pinned_preset_id?, pinned_preset_rev?, default_conflict, checksum_algo, reserve_bytes, archived, created_at, updated_at}`

`Preset` (portable, `schema: "ferry-preset-v1"`) = `{schema, name, description?, rules: [{id, when, template}], groups?: [{id, when, template}], exclusions?: [{id, when, reason}], fallback, empty_dirs?, conflict_policy: keep_both|skip_identical|needs_review}`. `when` = `{path_glob?, ext?, category?, source_label?, camera_role?, companion?}`. Template tokens: `{source_label} {relative_dir} {filename} {stem} {ext} {category} {mtime_date} {mtime_year} {mtime_month} {year} {month} {capture_date} {capture_year} {capture_month} {camera_role} {camera_model} {reel} {resolution_bucket} {seq} {seq:0Nd}`. `{a|b}` is an explicit fallback chain. Group templates may use only `{source_label} {relative_dir} {filename}`.

### Plans

| method | params | result |
|---|---|---|
| `plan.create` | `PlanRequest` | `PlanSummary` (status `draft`) |
| `plan.get` | `{plan_id}` | `PlanSummary` |
| `plan.entries` | `{plan_id, destination_id?, cursor?, limit?}` (default 200, max 1000) | `{entries: [PlanEntry], total, next_cursor?}`. Stable order; walk `next_cursor` until absent. |
| `plan.tree` | `{plan_id, destination_id, parent_path?, depth?, limit?, cursor?}` | `TreeNode` |
| `plan.resolve` | `{plan_id, revision: {camera_roles?, exclude?: [{source_label, rel, reason}], conflict_policy?, source_labels?: {index: label}}}` | the successor `PlanSummary`; the original is invalidated (`superseded_by`) |
| `plan.approve` | `{plan_id, fingerprint}` | `{status: "approved", plan_id}`, or, for a plan an agent parked in `awaiting_approval`, `{status: "started", transfer_id, actor}` (it starts under the agent's name). Human only: an `agent:` actor gets `-32002`. Approval re-checks the sources and the destination paths first and invalidates the plan if either changed. |

`PlanRequest` = `{sources: [{peer_id?, path, label?}], destinations: [{peer_id?, path?, destination_id?, label?, confirm_binding?}], preset_id?, preset_rev?, conflict_policy?, checksum_algo?: sha256|blake3, camera_roles?: {source_label: role}, exclude?: [{source_label, rel, reason}], actor?}`. A `peer_id` other than `local` names a paired machine.

`PlanSummary` = `{plan_id, fingerprint, status: draft|awaiting_approval|approved|executed|invalidated, actor, checksum_algo, destinations: [PlanDestination], findings: [{severity: blocking|warning, kind, plan_dest_id?, source_rel?, dest_rel?, message}], blocking_count, counts: {copy, dir, exclude, skip_identical, needs_review}, total_bytes, derived_from?, superseded_by?, invalidated_reason?}`

`PlanDestination` = `{id, destination_id?, label, peer_id, root_path, identity_snapshot?, storage_class, publish_strategy: hardlink|rename_noreplace|reserve_rename, free_bytes, required_bytes, reserve_bytes, preset_id, preset_rev, preset_hash, conflict_policy}`

`PlanEntry` = `{id, plan_dest_id, source_root, source_rel, source_path, source_label, source_peer, dest_rel_path, action: copy|dir|exclude|skip_identical|needs_review, matched_rule?, group_id?, conflict_kind?, exclusion_reason?, excluded_by_user?, camera_role?, size, mtime_ns, kind: file|dir, expected_hash?, renamed_from?, warning?, metadata?}`. `conflict_kind` is one of `duplicate_target`, `case_only`, `unicode_normalization`, `existing_file`, `same_name_different_content`, `existing_identical`, `file_vs_directory`, `ancestor_conflict`, `unsafe_destination_path`, `group_root_collision`, `unsupported_object`.

`TreeNode` = `{name, path, kind: dir|file, file_count, total_bytes, conflict_count, renamed_count, excluded_count?, matched_rule?, sample_transforms?: [{before, after, action}], before?, action?, conflict_kind?, renamed_from?, child_count, children?: [TreeNode], next_cursor?}`. Children are directories first, then files, by name. To page, call again with `parent_path` = that node's `path` and its `next_cursor`.

Local and peer transfers use the checksum in the reviewed plan for both the source digest and the physical readback. SHA-256 is the compatible choice for peers running an earlier v2 daemon. BLAKE3 peer transfers require support at both ends.

### Transfers

| method | params | result |
|---|---|---|
| `transfer.start` | `{plan_id, fingerprint, actor?}` | `{status: "started", transfer_id, actor}` · `{status: "awaiting_approval", plan_id, reasons}` · `{status: "needs_attention", transfer_id, reason}` (another transfer holds one of the paths) |
| `transfer.list` | `{limit?}` | `[{id, plan_id, status, actor, started_at, ended_at, error_summary}]` newest first |
| `transfer.get` | `{transfer_id}` | `{transfer, targets: [TransferTarget], item_counts, failures: [{dest_rel, target_id, error}], running, progress?: Progress, receipt_id?, receipt_path?}` |
| `transfer.cancel` | `{transfer_id}` | `{cancel_requested: true}`. Cancellation is acknowledged immediately; the final `transfer.state` (`canceled`) arrives when the in-flight chunk stops. |
| `transfer.resume` | `{transfer_id}` | `{transfer_id, status: "resuming"}`; it reconciles the crash windows, never re-copies a verified file |

**Agent policy.** For an `agent:` actor, `transfer.start` consults `[agent_policy]`. A destination that is not a saved destination (unless `allow_unsaved_destinations`), a saved destination when `auto_approve_saved_destinations` is off, or a total over `auto_approve_max_bytes` parks the plan in `awaiting_approval`. No transfer is created, and a `plan.awaiting_approval` event fires until a human calls `plan.approve`. The approval is single-use: starting a transfer moves the plan to `executed`.

`TransferTarget` = `{id, transfer_id, plan_dest_id, label, peer_id, root_path, transport_kind: local|p2p, publish_strategy, readback_mode: f_nocache|fadvise_dontneed|remote_readback, status, bytes_written, bytes_verified, files_done, files_failed, ema_write_bps, ema_verify_bps, error?, started_at, ended_at}`

Transfer status: `queued → running → completed | partial | failed | canceled | needs_attention`. `needs_attention` keeps its path reservations and is resumable.

### Receipts and verification

| method | params | result |
|---|---|---|
| `receipt.list` | `{transfer_id?, limit?}` | `[{receipt_id, transfer_id, plan_id, status, actor, digest, exported_path?, export_error?, created_at}]` |
| `receipt.get` | `{receipt_id}` or `{transfer_id}` (latest) | the `ferry-receipt-v2` document |
| `receipt.export` | same | `{receipt_id, path}` (retries a failed export; never overwrites a different file) |
| `verify.run` | `{receipt_id or transfer_id, target_id?}` | `{receipt_id, transfer_id, checksum_algo, readback_mode, targets: [{target_id, label, root, checked, missing, modified, added, unreadable}], checked, missing, modified, added, unreadable, clean, exit_code}`. Targets on peers are re-hashed by the peer itself with its own physical readback. Exit codes: 0 clean, 1 missing, 2 modified, 3 added, 4 could not verify (outranks the rest). |

## Events

`Event` = `{topic, time, data}`

| topic | when | data |
|---|---|---|
| `transfer.progress` | every 150 ms while a transfer runs (coalesced, never per chunk), plus a final one | `Progress` |
| `transfer.state` | each status change | `{transfer_id, status, actor}` |
| `transfer.finished` | the run returned | `{transfer_id, status, receipt_id?, receipt_path?, export_error?, findings?}` |
| `transfer.error` | the run could not finish (e.g. resume refused) | `{transfer_id, error}` |
| `plan.awaiting_approval` | an agent's start was held by policy | `{plan_id, actor, reasons}` |
| `drive.mounted` | a volume appeared (polled every 3 s) | `Drive` |
| `drive.unmounted` | a volume disappeared | `{mount_path}` |
| `peer.changed` | paired, or the active path changed (failover) | `{peer_id, state?, transport_badge?, addr?, failover?}` |
| `verify.finished` | `verify.run` finished | `{receipt_id, clean, exit_code}` |

`Progress` = `{transfer_id, actor, overall_fraction, bytes_written, bytes_verified, total_bytes, eta_seconds, targets: [{target_id, label, peer_id, transport_badge, current_file?, phase: streaming|verifying|publishing|idle|done, bytes_written, bytes_verified, total_bytes, write_bps, verify_bps, eta_seconds, done_files, failed_files, total_files}]}`

ETA is two-phase per target: `remaining_write / write_bps + remaining_verify / verify_bps`. Each rate is a 1-second-bucket EMA (α = 0.25), seeded from the storage class until 2 s of real data exist. Whole transfer ETA = the slowest target. `-1` means unknown. Remaining work never shows as `0` (it rounds up). `overall_fraction` weighs writing and verifying equally.

Delivery is best-effort per subscriber: a client that stops reading loses events (counted in `daemon.status.events_dropped`). It never stalls a transfer and never loses an RPC reply. After reconnecting, re-read state with `transfer.get` / `daemon.status`.

## MCP mapping

`ferry mcp` serves MCP over stdio (protocol versions `2025-06-18`, `2025-03-26`, `2024-11-05`) and forwards to the methods above as `agent:<client name>`:

| tool | method |
|---|---|
| `ferry_status` | `daemon.status` |
| `ferry_drives` | `drive.list` |
| `ferry_peers` | `peer.list` |
| `ferry_destinations` | `dest.list` |
| `ferry_presets` | `preset.list` |
| `ferry_plan` | `plan.create` |
| `ferry_plan_tree` | `plan.tree` |
| `ferry_transfer_start` | `transfer.start` |
| `ferry_transfer_status` | `transfer.get` |
| `ferry_transfer_cancel` | `transfer.cancel` |
| `ferry_receipt` | `receipt.get` |
| `ferry_verify` | `verify.run` |

There is no tool, and no method, that deletes, moves, renames or overwrites user files. Agents cannot call `plan.approve`.
