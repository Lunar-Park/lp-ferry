# Rename & Cleanup Plan: media-mate → file-ferry

## Overview

Rename the project from `media-mate` to `file-ferry` and make the DaVinci Resolve integration an optional dependency. TUI stays bundled. No feature removal — just cleaner packaging.

> **Later move — 2026-10-28.** The repo moved from the personal account
> `dspury/file-ferry` to the organization `Lunar-Park/lp-ferry`. See
> ["Second rename: repo moved to the Lunar-Park org"](#second-rename-repo-moved-to-the-lunar-park-org)
> at the end of this document. The plan below is the record of the
> `media-mate` → `file-ferry` rename and is left as it was written.

---

## Phase 1: Rename

### 1.1 Package identity

| What | Old | New |
|------|-----|-----|
| PyPI name | `media-mate` | `file-ferry` (`ferry` is taken on PyPI) |
| GitHub repo | `dspury/media-mate` | `dspury/file-ferry` |
| Python package | `media_mate` | `file_ferry` |
| CLI command | `media-mate` | `ferry` |
| Import | `from media_mate import ...` | `from file_ferry import ...` |

### 1.2 Files to update

- [x] `pyproject.toml` — name, version → 0.3.0, scripts entry point
- [x] `src/media_mate/` → `src/file_ferry/` (rename directory)
- [x] All internal imports (`from media_mate.X` → `from file_ferry.X`)
- [x] `README.md` — all references, install instructions, badges, CLI examples
- [x] `CHANGELOG.md` (if exists) — note the rename *(no CHANGELOG.md; recorded as `SPEC.md` §19 "v0.3.0 — Rename to ferry")*
- [x] CI workflow (`.github/workflows/ci.yml`) — any hardcoded paths or package names
- [x] Tests — all imports and references
- [x] `LICENSE` — check if name is referenced *(checked: no project name in it, unchanged)*
- [x] Examples (`examples/`) — any scripts or docs referencing the old name

### 1.3 GitHub

Done by **renaming** the repo, not by forking it — matching "What stays the
same" below rather than the create-new-repo sketch this section originally had.
`gh repo rename ferry` kept all 45 issues, 17 PRs, both tags and the commit and
CI history, and GitHub installs a permanent redirect.

- [x] ~~Create new repo~~ → renamed `dspury/media-mate` → `dspury/ferry` → `dspury/file-ferry`
- [x] Push code to the renamed repo
- [x] ~~Archive `dspury/media-mate` with redirect notice~~ → **not applicable.**
      Renaming leaves no second repo to archive: `github.com/dspury/media-mate`
      returns `301` to the new URL, and `git clone` / `git push` against the old
      remote kept working. Careful, *while that was true* — the GitHub API
      follows a rename redirect, so `gh repo archive media-mate` would have
      archived `dspury/ferry` itself. The placeholder below now occupies the
      old name, so the redirect (and the hazard) is gone.
- [x] Update any KB docs pointing to old repo
- [x] Re-create **both** freed names — `dspury/media-mate` and `dspury/ferry`
      (freed when the repo was renamed a second time, to `dspury/file-ferry`) —
      as **private, archived placeholders** so
      the old name cannot be claimed. Created private from the start (never
      briefly public), each given a README that says where the project went,
      then archived. This **ends** the redirects rather than adding to them —
      old links now 404 for anyone but D — which was the accepted trade: the
      names had barely been shared. Side benefit: each old name now resolves to
      its own placeholder, so the `gh repo archive <old-name>` foot-gun above
      is gone.

### 1.4 PyPI

- [ ] Register `file-ferry` on PyPI (first publish)
- [ ] Optionally yank or deprecate `media-mate` if it was ever published

---

## Phase 2: Optional Resolve Dependency

### 2.1 Current state

Resolve is already well-structured:
- `resolve.py` has a **manifest-first** architecture — pure functions (`build_project_manifest`, `write_manifest`) need no Resolve runtime
- The Resolve API adapter (`find_resolve`, `create_resolve_project`) is best-effort and already handles "Resolve not installed" gracefully
- TUI already lazy-imports resolve inside `_run_resolve_step()`
- CLI imports at top level — needs to become lazy

### 2.2 Changes

**pyproject.toml:**
```toml
[project.optional-dependencies]
resolve = []  # No extra deps — Resolve is detected at runtime via DaVinciResolveScript
tui = []      # Textual stays in core deps for now; placeholder if split later
```

Actually — Resolve has no pip dependency. It's detected at runtime. So `optional-dependencies` for resolve is really about documentation and intent, not pip packages. The real change is:

**cli.py:**
- [x] Move `from media_mate.resolve import create_resolve_project` from top-level to inside `resolve_create()` function
- [x] Add a helpful error message if resolve module fails to import (shouldn't happen since it's in-package, but good practice)

**tui.py:**
- [x] Already lazy — no change needed. Just verify the import guard is clean.

**The actual isolation is about making the resolve step skippable and clearly optional in docs/UI, not about removing code.**

### 2.3 Documentation

- [x] README: mark Resolve as optional ("requires DaVinci Resolve installed separately")
- [x] CLI help text: note that `--resolve-project` requires DaVinci Resolve
- [x] TUI: the Resolve checkbox already exists — add tooltip or note that it's optional

---

## Phase 3: Cleanup (while we're in here)

### 3.1 Minor cleanup

- [x] Version: v0.3.0 (new name, continuous from media-mate)
- [x] `__init__.py` — update `__version__` and any public API exports
- [x] Ruff format pass after all renames
- [x] Ensure all tests pass with new package name

### 3.2 KB updates

- [x] `lunar-park-kb/projects/open-source-projects/media-mate/README.md` → rename folder + update all refs
- [x] Issue pipeline assignment table — update repo name
- [x] Any other KB docs referencing `media-mate`

---

## What stays the same

- All features — probe, organize, proxy, resolve, verify, log, TUI, CLI
- All tests (just import paths change)
- TUI stays bundled (not split out)
- Resolve stays in-package, just documented as optional
- Same repo history (rename, don't fork)

---

## Decisions

- [x] **Version number** — v0.3.0 (continuity from media-mate)
- [x] **GitHub org** — `dspury/file-ferry` (personal) — *superseded 2026-10-28;
      the repo now lives at `Lunar-Park/lp-ferry` (org)*
- [x] **CLI command** — `ferry`

---

## Execution notes (Phases 1–3, done)

Phases 1.1/1.2, 2 and 3 are landed on branch `rename/ferry`, on top of the
merged PRs #61 and #62. Only §1.3 (GitHub) and §1.4 (PyPI) remain, and those
are D's to run.

### Two names, and the rule that separates them

This landed in three steps. The plan opened with `ferry-media` for the package
and `ferry` for the command; that was rejected in favour of `ferry` everywhere;
then `ferry` turned out to be taken on PyPI (an unrelated package abandoned in
2014, which PyPI will not reclaim), and the project settled on **`file-ferry`**
— which describes the tool better than a bare `ferry` anyway.

The rule, applied consistently:

- **`file-ferry` / `file_ferry` / "File Ferry" — the project.** PyPI
  distribution, Python import package, GitHub repo, npm package, document
  titles, and prose that refers to the project itself.
- **`ferry` / "Ferry" — what you type and see.** The CLI command and the things
  named after it: `ferry.toml`, `~/.ferry/`, `ferry.db`, `FERRY_*`,
  `Application Support/ferry/`, `ferry-service`, `window.ferry`, `FerryConfig`,
  the Electron `productName` and window title, the TUI banner, and the product
  as spoken about in running prose. The only compounds left
are the ones that need a suffix to disambiguate a second artifact:
`ferry-service` (the sidecar binary) and `ferry-desktop` (the private npm
package for the Electron shell).

### Names RENAME.md did not spell out

| What | Old | New |
|------|-----|-----|
| Service entry point | `media-mate-service` | `ferry-service` |
| Config model class | `MediaMateConfig` | `FerryConfig` |
| Preload API type | `MediaMateAPI` | `FerryAPI` |
| Preload bridge key | `window.mediaMate` | `window.ferry` |
| Textual theme name | `media-mate-studio` | `ferry-studio` |
| Electron `productName` | `media-mate` | `ferry` |
| Electron `appId` | `io.github.dspury.media-mate` | `io.github.dspury.ferry` |
| npm package | `media-mate-desktop` | `file-ferry-desktop` |
| Project config file | `media-mate.toml` | `ferry.toml` |
| Home config / audit db | `~/.media-mate/media-mate.db` | `~/.ferry/ferry.db` |
| Desktop app data (macOS) | `~/Library/Application Support/media-mate/` | `~/Library/Application Support/ferry/` |
| Db backup prefix | `media-mate-{ISO8601}-pre-{NNN}.db` | `ferry-{ISO8601}-pre-{NNN}.db` |
| Env overrides | `MEDIA_MATE_{CONFIG,DB,PROTOCOL_VERSION}` | `FERRY_{CONFIG,DB,PROTOCOL_VERSION}` |
| Diagnostic export | `media-mate-diagnostics-*.txt` | `ferry-diagnostics-*.txt` |
| Plan/direction docs | `docs/MEDIA-MATE-*.md` | `docs/FERRY-*.md` |
| Config example | `media-mate.toml.example` | `ferry.toml.example` |

### Also done in this pass

- **`resolve` and `tui` extras added** to `pyproject.toml` per §2.2. Neither
  installs anything — the comment above them says why — so they are a statement
  of intent, not a dependency edge.
- **Upgrade note in the README** ("Upgrading from media-mate (v0.2.x)"): the two
  `mv` commands that carry `~/.media-mate` and a project `media-mate.toml` over,
  plus what is lost if the desktop app-data directory is left behind. There is
  no automatic migration and nothing reads the old paths.
- **`docs/archive/` deleted** — `SPEC_v0.2.2.md`, `architecture.md`,
  `sample-run.md`. The README link to it is gone; the `.gitignore` rule stays as
  a guard against re-adding an archive by accident.
- **Resolve made lazy at *two* CLI call sites**, not one. §2.2 names
  `resolve_create()`; `run --resolve-project` used the same top-level import.
  Both now go through `cli._load_create_resolve_project()`.

### Known open items — fixed in a follow-up pass

All four are done; see the "desktop shell boot path" commit. Fixing them
surfaced five further defects that had never been reachable because the shell
had never started. Full list in that commit message; the security-relevant one
is written up as [ADR-0006](docs/adr/0006-development-csp-relaxation.md).

---

## Second rename: repo moved to the Lunar-Park org

**Date:** 2026-10-28. **Type:** GitHub organization move, not a package rename.

The repository moved from the personal account `dspury/file-ferry` to the
organization `Lunar-Park/lp-ferry`, alongside the rest of the Lunar Park
open-source portfolio. GitHub installed a permanent redirect, so the old URL
still resolves and existing clones keep working.

### What did not change

- **The package.** `file-ferry` on PyPI, `file_ferry` as the import name,
  `ferry` as the command, `ferry-service` as the service entry point. The
  two-name rule in SPEC.md §14 stands unchanged — `file-ferry` is the project,
  `ferry` is what you type.
- **The code.** No source file was touched by the move.
- **The version.** Still `0.3.0` in `pyproject.toml`, still untagged and
  unreleased.

### What did change

- **The clone directory name.** Cloning the new URL produces a directory named
  `lp-ferry`, where it used to produce `file-ferry`. Every `git clone` line in
  `README.md` and `CONTRIBUTING.md` is now paired with a `cd` into the
  directory that clone actually creates, so the quickstart cannot break.
- **The canonical URL.** `README.md`, `CONTRIBUTING.md`, `pyproject.toml`
  (`[project.urls]`) and `SPEC.md` now name `Lunar-Park/lp-ferry`.

### What was deliberately not done

- **No release, no tag, no publish.** The `file-ferry` name on PyPI is still
  unregistered; there is still no `v0.3.0` tag and no GitHub release. Those
  are D's decision and were not taken as part of the move.
- **The Electron `appId` was left alone.** It is still
  `io.github.dspury.file-ferry`. Changing an app identifier that has already
  shipped in a build is a packaging decision with upgrade consequences, not a
  documentation one, so it is out of scope for this change.
- **The `media-mate` → `file-ferry` history above was not rewritten.**
