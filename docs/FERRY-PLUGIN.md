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
| `ferry.plan.get` | read | One plan with every file in it, or the first `items_limit` of them |
| `ferry.plan.list` | read | Plans, newest first |
| `ferry.source.list` | read | Source folders ferry has planned from, and whether each is mounted |
| `ferry.ingest.list` | read | The Ingest view's one read: the sources, and one row per offload with its newest transfer and receipt joined (plus a row per proxy or Resolve receipt) |
| `ferry.offload` | write | Queue a plan's copy as a `ferry.copy` job and record the transfer |
| `ferry.transfer.get` | read | One transfer with its progress |
| `ferry.transfer.list` | read | Transfers, newest first |
| `ferry.transfer.start` | write | Mark a transfer running (the copy job's first call) |
| `ferry.transfer.progress` | write | Record how far a transfer has got, a few percent at a time |
| `ferry.receipt.record` | write | Write the receipt for a job; a copy receipt also closes its transfer |
| `ferry.receipt.get` | read | One receipt, with every file, hash and destination |
| `ferry.receipt.list` | read | Receipt summaries, newest first |
| `ferry.send` | write | Send a folder from one machine's disk to another's: queue the stage job on the source machine |
| `ferry.send.land` | write | Called by the stage job: plan the landing and queue the land job on the destination machine |
| `ferry.send.fail` | write | Called by the stage job when it cannot reach a plan: end the transfer with the reason |
| `ferry.verify` | external | Hash a transfer's destinations again and compare with its receipt |
| `ferry.proxies` | write | Queue a `ferry.proxy` job for the video in a folder |
| `ferry.resolve_project` | write | Record a Resolve project request and queue a `ferry.resolve` job |

## Jobs

| Job | Needs | What it does |
|---|---|---|
| `ferry.copy` | nothing | Copies each planned file with a hash check on the way in and out, then registers it with `job.register_output` so it lands as an Asset by hash. A mismatch is reported, not counted as copied |
| `ferry.stage` | nothing | On the source machine: hashes every file in place and registers it as an Asset whose path is on that machine. Copies nothing, writes nothing to the source. Declares `egress = "none"` |
| `ferry.land` | nothing | On the destination machine: fetches each staged file by hash straight from the source machine's file server (`fetch_asset(..., from_machine=)`), writes it under the destination, hashes it again, and registers it. A file that arrives with another hash is deleted and reported. Existing destinations are preserved. Declares `egress = "lan"` |
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

## Sending between machines

`ferry.send` moves a folder from one machine's disk to another's with every file hashed at both
ends, a receipt that names both machines, and every step an op an agent can call.

```
ferry.send -> stage job (source machine) -> ferry.send.land -> land job (destination machine)
```

The stage job hashes and registers the source in place. The land job takes each file straight from
the source machine's node file server; the hub only says where that server is and carries no bytes,
and nothing is staged on a third machine. A send plan is an ordinary plan row owned by the
destination machine, and its items carry the hash the source computed in their `probe` object, so no
table changed. The receipt is a `copy` receipt whose `detail.send` names both machines, which is what
`ferry.verify` and the Ingest view already read.

Needs a Park with `fetch_asset(..., from_machine=)`, the source machine's
node file server on and reachable from the destination (`serve_files`, a `bind` address the destination
can reach, a correct `advertise_url`), and Ferry loaded on both nodes. A send never deletes the source.
Not yet: a grant scoped to one transfer (today any valid node token can fetch a file by hash from a
file server; the traffic is plain HTTP, so enable a file server only on a network you control — see
`SECURITY.md`), a pre-check of the destination before the source is hashed, and Windows destinations.

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
`/plugins/ferry/views/ingest.js`. park mounts it as the Library's Ingest tab. It draws from one
read, `ferry.ingest.list`: a dense list with one row per offload (card, where it lands, size, and
where it stands), grouped as in progress, needs a look, planned and done, with the cards in the
sidebar as a filter. A row opens in the inspector (paths, the copy's figures, problems, and a page
of the plan's files from `ferry.plan.get`). The page's one action plans a card (`ferry.plan`);
a planned offload is queued, and a failed one retried, from its inspector (`ferry.offload`).
Events on `ferry.plan.created`, `ferry.transfer.*` and `ferry.receipt.recorded` patch the rows
they name, so progress costs no request.

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
