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
- **Next:** Phase 2 (presets, camera/metadata, planner, `plan.tree`).

## Where things are

- `internal/storage/containment.go`: `openat(O_NOFOLLOW)` walk, root binding (dev+inode), exclusive create.
- `internal/storage/publish*.go`: hardlink, rename_noreplace or reserve_rename, chosen by probe.
- `internal/storage/readback_*.go`: `F_NOCACHE` / `FADV_DONTNEED`.
- `internal/engine/runner.go`: create, run, resume, crash-window reconcile, receipts.
- `internal/engine/engine_test.go`: T1.1–T1.7. `cmd/ferry/verify_test.go`: T1.8.
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
- Phase-1 limits, made explicit rather than faked:
  - Remote targets are refused at create (Phase 3).
  - `skip_identical` / `needs_review` entries are refused at transfer start (Phase 2).
  - No hot-plug watcher or link speed yet.
  - The `verifying` transfer state isn't emitted.
