# Ferry — working note

## Intent

Rebuild Ferry's backend as a single Go binary, following
[`docs/GO-BACKEND-REWRITE-PLAN.md`](docs/GO-BACKEND-REWRITE-PLAN.md) phase by
phase on the local branch `feat/go-v2-backend`. Nothing is pushed until D says
so. The Python/Electron code stays untouched until Phase 5. Tag
`legacy-python-v0.3` (local) marks the state before the rewrite.

## State (2026-11-22)

- **Phase 0 done:** `go.mod`, strict `internal/config`, `Makefile`, Go CI job, static cross-builds for linux/amd64, linux/arm64, darwin/arm64 and darwin/amd64.
- **Phase 1 done:** `internal/storage`, `identity`, `ledger`, `engine`, `receipt`, `verify`, plus `ferry verify`. T1.1–T1.8 pass under `go test -race` on macOS, with 20× stress runs on engine, ledger and storage.
- **Linux:** vetted, linted and test-compiled, but **not executed** (Docker wasn't running). The CI ubuntu leg runs them on the first push.
- **Phase 2 done:** `internal/metadata`, `preset`, `planner`, migration 0002. T2.1–T2.6 pass under `-race`.
- **Phase 3 done:** `internal/p2p`, migration 0003, engine push/pull. T3.1–T3.5 pass under `-race` on loopback (real Ed25519 mTLS 1.3, real mDNS). **T3.6 (real two-machine run) is held until D approves deployment.**
- **Phase 4 done:** `internal/daemon`, `ipc`, `policy`, `mcp`, the full `cmd/ferry` CLI and [`docs/IPC-PROTOCOL.md`](docs/IPC-PROTOCOL.md). T4.1–T4.3 pass under `-race`, and the built binary was smoke-tested end to end (daemon, doctor, send, verify, MCP over stdio, SIGTERM).
- **Phase 5, non-destructive half done:**
  - packaging: `packaging/systemd/ferryd.service` (notify, hardened), `packaging/launchd/com.lunarpark.ferryd.plist`, `packaging/ferry.toml.example` (tested to load)
  - `.goreleaser.yaml`: validated, with a snapshot build; legacy tags ignored, snapshots versioned `2.0.0-dev+<commit>`
  - `ferry doctor` works without a daemon
  - `docs/GO-QUICKSTART.md`, plus a README pointer
- **Held for D:**
  - deleting `desktop/`, `src/file_ferry/`, the legacy tests and scripts
  - converting `park_plugins/ferry/` (~3.3k lines, with proxy/Resolve jobs and tests that call `jobs.stage`/`jobs.land`) into a thin adapter
  - the full README rewrite
  - any push

## Where things are

- `internal/storage/containment.go`: `openat(O_NOFOLLOW)` walk, root binding (dev+inode), exclusive create.
- `internal/storage/publish*.go`: hardlink, rename_noreplace or reserve_rename, chosen by probe.
- `internal/storage/readback_*.go`: `F_NOCACHE` / `FADV_DONTNEED`.
- `internal/engine/runner.go`: create, run, resume, crash-window reconcile, receipts.
- `internal/engine/engine_test.go`: T1.1–T1.7. `cmd/ferry/verify_test.go`: T1.8.
- `internal/metadata`: MP4/MOV, EXIF, BWF/iXML, Sony XML readers; card fingerprinting; vendor-pattern companions.
- `internal/preset`: glob dialect, templates, routing engine, built-ins, immutable revisions.
- `internal/planner`: scanner, conflict matrix, approve/revise, `plan.tree`. Tests: `planner_test.go` (T2.x).
- `internal/p2p`: identity, pairing, grants, Path Radar, HTTP/2 handlers, pull/push, mDNS, plus the adapter implementing the planner and engine `Remote` interfaces. Tests: `p2p_test.go` (T3.x).
- `internal/daemon`: service wiring, every RPC method, event bus, drive watcher. `internal/ipc`: socket JSON-RPC. `internal/mcp`: 12-tool stdio server. `internal/policy`: agent rules. Tests: `daemon_test.go` (T4.x), `cmd/ferry/cli_test.go`.
- Gate: `make vet lint race cross`.

## Decisions

- Go directive is 1.26, because current modernc/x/sys need it. This still meets "1.24+".
- The Go CI job sits beside the Python and desktop jobs, so the required check names on `main` stay stable until Phase 5.
- `make` targets `./cmd/... ./internal/...`, because a Go package inside `desktop/node_modules` breaks `./...`.
- Destination binding is the root's device **and inode**. A leftover mount directory differs even on the same device, which is how T1.4 is tested without a real mount.
- Containment walks with `openat(O_NOFOLLOW)` from the root fd rather than lstat-then-path, so there's no check-then-use window.
- Path reservations fold case and Unicode NFC, so case-only and NFC/NFD twins collide.
- Receipts export to `~/.ferry/receipts/<id>.json` (no-clobber), not into the destination. There is one receipt per terminal outcome, so a resumed transfer gets a new one.
- `ferry verify` exit 4 means "could not verify" (unreadable file, unreachable root, or invalid/edited receipt) and outranks 1/2/3. "Added" only looks in folders the receipt wrote into, ignoring dot-files and volume litter.
- A transfer start consumes its plan's approval (plan → `executed`). Edits to a preset, saved destination, camera binding or camera role invalidate pending plans in the same transaction.
- Companion sidecars pair only by documented vendor naming (Sony `M01.XML`, GoPro `.THM`/`.LRV`, DJI `.SRT`/`.LRF`). Nothing is paired by guessing from a shared basename.
- A missing `{capture_date}` routes to the fallback with a warning finding. `{a|b}` alternatives are opt-in, and the fallback template must always render.
- macOS volume identity comes from `statfs` `f_mntonname`, because firmlinks put `/private/var` on the Data volume, not the sealed `/`. Mount writability is the read-only flag, and source/destination overlap is checked by inode.
- P2P trust is pinned Ed25519 keys only: no CA, and `VerifyConnection` with session tickets off, so every handshake re-proves the key. Pairing is a single-use 128-bit secret proven by HMAC over the TLS exporter on both sides.
- Grants are signed by the source and bound to transfer, grantee, item list and expiry. Items are rechecked against allowed roots with lstat containment on every request, and the client refuses all redirects.
- Push is used when the source is local (the card is read once for every target). Pull/land is used when the source is a peer, so a coordinator carries no file bytes. A pull resumes from the job-owned temp's size, and the source hashes the prefix locally so the trailer is always a whole-file SHA-256.
- Path Radar retries a downed path only when nothing else answers, so a brief drop on the only link resumes instead of failing.
- IPC is newline-delimited JSON-RPC 2.0 on a 0600 socket in a 0700 directory, with a same-uid peer-credential check. Params reject unknown fields. Event delivery is best-effort per subscriber and never stalls a transfer.
- Agents act as `agent:<name>`. Over-policy requests (unsaved destination, over `auto_approve_max_bytes`, or saved-destination auto-approve off) park the plan in `awaiting_approval`. A human `plan.approve` starts it under the agent's name, and agents cannot approve. Actor strings are attribution plus a policy hook, not authentication against same-user code.
- MCP is a lean in-repo stdio implementation (no SDK dependency), with 12 tools all marked `destructiveHint: false`. Nothing anywhere deletes, moves or overwrites.
- The final `transfer.state` is emitted after the receipt export, so a client reacting to it can open the receipt.
- Drive hot-plug is a 3 s mount-list poll that emits `drive.mounted` / `drive.unmounted`. There are no FSEvents/udev hooks yet.
- Phase-1 limits, made explicit rather than faked:
  - Remote targets: `skip_identical` isn't offered (keep_both instead). Remote crash-window reconcile re-checks on the peer via `/v1/verify`.
  - `needs_review` entries are refused at approval and at transfer start. `skip_identical` re-proves the checksum at run time.
  - Link speed: Linux reads sysfs speed; macOS tiers by interface name only.
  - The `verifying` transfer state isn't emitted.
