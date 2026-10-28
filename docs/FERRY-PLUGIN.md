# Ferry as a park plugin

Ferry ships twice: the `ferry` CLI in `src/file_ferry/` and the plugin in
`park_plugins/ferry/`. This page describes the plugin. The CLI stays as it is until the
cutover, so nothing that runs today depends on the plugin being loaded.

The plugin has no third-party runtime dependency. `config`, `probe`, `layout` and `copy`
are standard library only and import without park; `records` and `ops` are what park
loads, and `jobs` imports park only for types.

## Ops

Every op is `ferry.<...>`. Reads return rows, writes emit events, external ops touch the
disk outside park's write lock.

| Op | Effect | What it does |
|---|---|---|
| `ferry.plan` | external | Scan a source folder, probe the media, render a destination for every file, record the plan |
| `ferry.plan.get` | read | One plan with every file in it |
| `ferry.plan.list` | read | Plans, newest first |
| `ferry.source.list` | read | Source folders ferry has planned from, and whether each is mounted |
| `ferry.offload` | write | Queue a plan's copy as a `ferry.copy` job and record the transfer |
| `ferry.transfer.get` | read | One transfer with its progress |
| `ferry.transfer.list` | read | Transfers, newest first |
| `ferry.transfer.start` | write | Mark a transfer running (the copy job's first call) |
| `ferry.transfer.progress` | write | Record how far a transfer has got, a few percent at a time |
| `ferry.receipt.record` | write | Write the receipt for a job; a copy receipt also closes its transfer |
| `ferry.receipt.get` | read | One receipt, with every file, hash and destination |
| `ferry.receipt.list` | read | Receipt summaries, newest first |
| `ferry.verify` | external | Hash a transfer's destinations again and compare with its receipt |
| `ferry.proxies` | write | Queue a `ferry.proxy` job for the video in a folder |
| `ferry.resolve_project` | write | Record a Resolve project request and queue a `ferry.resolve` job |

## Jobs

| Job | Needs | What it does |
|---|---|---|
| `ferry.copy` | nothing | Copies each planned file with a hash check on the way in and out, then registers it with `job.register_output` so it lands as an Asset by hash. A mismatch is reported, not counted as copied |
| `ferry.proxy` | `ffmpeg` | Encodes a ProRes proxy per item and registers each proxy |
| `ferry.resolve` | nothing | Writes the project manifest and, when Resolve's scripting module is installed, creates the project. The manifest is the deliverable when it is not |

`ferry.copy` needs the source mounted where the job runs; that is checked per file and
reported as `source is not mounted`, so a job that lands on the wrong node fails loudly
instead of copying nothing.

## Events and tables

| Event | Table |
|---|---|
| `ferry.plan.created` | `ferry_plans`, `ferry_plan_items` |
| `ferry.transfer.queued` | `ferry_transfers` |
| `ferry.transfer.started` | `ferry_transfers` |
| `ferry.transfer.progress` | `ferry_transfers` |
| `ferry.transfer.finished` | `ferry_transfers` |
| `ferry.receipt.recorded` | `ferry_receipts` |
| `ferry.verify.recorded` | `ferry_verifications` |
| `ferry.proxies.requested` | `ferry_proxy_requests` |
| `ferry.resolve.requested` | `ferry_resolve_requests` |

The plan event carries every file, so a plan is one event and one row per file. A receipt
carries every file it records, with the SHA-256 of what landed; `ferry.verify` reads that
receipt as its baseline.

## `ferry.toml`

The plugin reads the same file as the CLI: an explicit `config_path`, then `./ferry.toml`,
then `~/.ferry/config.toml`, then defaults.

- Unknown keys are refused, at the top level and inside `[organize]` and `[proxy]`, before
  any work starts. A typo cannot quietly keep a default in place.
- `[proxy]` may hold `proxy_codec`, `proxy_height` and `proxy_backend`; the top level stays
  canonical.
- `organize.template` must contain `{root}` and `{filename}`, and may use `{source_relpath}`,
  `{ext}`, `{date}`, `{codec_family}` and `{resolution_bucket}`.
- `organize.mode = "move"` is refused: the plugin copies.
- `checksum_algo` is accepted so an existing config loads, but identity is always SHA-256,
  because that is what park's Assets are keyed by.
- `proxy_backend` is accepted so an existing config loads, and proxies are encoded with
  ffmpeg. Each proxy receipt records `backend: ffmpeg` rather than implying otherwise.

## The Ingest view

`views/ingest.js` exports `mount(el, api)` and is served at
`/plugins/ferry/views/ingest.js`. It shows the sources ferry has planned from, the plans,
the transfers and their progress, and the receipts. It reads the ops above and refreshes
on `ferry.plan.created`, `ferry.transfer.*` and `ferry.receipt.recorded`.

## What the plugin does not do yet

- It does not divide a plan across several transfers; one plan is one transfer.
- It does not encode with AVFoundation. The CLI's `proxy_backend = "auto"` preference is
  not carried over.
- It does not reverse a transfer, organise into a template other than the configured one,
  or move media.
- `ferry.offload`, `ferry.proxies` and `ferry.resolve_project` queue jobs through
  `machines.job.submit` when that op exists, and otherwise record the work with
  `queued: false`. The call shape ferry expects is pinned by `tests/park_plugin/test_machines.py`.

## Tests

`tests/park_plugin/` skips when park is not installed, which is the case in this repo's CI
matrix (3.11 to 3.13, no park). To run them:

```sh
uv venv -q -p 3.14 .venv
uv pip install -q -p .venv pytest -e /path/to/park -e .
.venv/bin/python -m pytest tests/park_plugin -q
```
