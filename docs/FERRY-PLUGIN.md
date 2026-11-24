# Ferry as a Park plugin

`park_plugins/ferry/` is a thin adapter between Park and the ferry engine, the Go `ferry daemon` running on the same machine (see [GO-QUICKSTART.md](GO-QUICKSTART.md)).

**What Park keeps:** its ops, events, records, project filing, the job queue and the Ingest view.

**What the engine does:** everything that touches bytes. That covers planning, the verified copy, machine-to-machine transport and re-verification. The plugin never copies a file or decides where one lands.

**What stays in Python:** proxies and Resolve projects.

The plugin has no third-party runtime dependency, and only `records` and `ops` import Park.
- **`engine`:** the daemon client: JSON-RPC over `$FERRY_SOCKET`, or `~/.ferry/ferry.sock` by default.
- **`config`, `probe`, `layout`:** standard library only.
- **`jobs`:** imports Park only for types.

## How a card offload runs

```
ferry.plan   -> engine plan.create (mirror-source preset)  -> ferry.plan.created
ferry.offload-> ferry.copy job on the owning machine
ferry.copy   -> engine transfer.start, progress relayed     -> ferry.transfer.*
             -> engine receipt, each file registered in Park -> ferry.receipt.recorded
```

**The plan:**
- **Identity:** the Park plan *is* the engine's plan. It has the same id and the same fingerprint, so the copy job starts exactly what was reviewed.
- **Approval:** starting the plan consumes the engine's single-use approval. Before anything moves, the engine re-checks every source and every destination path.
- **What a stale plan means:** a file that changed, or one that appeared at the destination, means the plan has to be made again. Nothing is copied over it.

**The copy:** for each file the engine writes a job-owned temp file, fsyncs it, reads it back from disk with the cache bypassed, compares hashes, and publishes it without replacing anything.

**Park's cross-check:** the job then registers each landed file with Park, which hashes it independently. A file counts as verified only when both hashes agree.

**The receipt:** a Park copy receipt carries `detail.engine`, which holds the engine's transfer, receipt, plan, status and digest. It also carries `detail.engine_receipt`, the full `ferry-receipt-v2`.

## Sending between machines

```
ferry.send -> pair the two engines once (ferry.engine call on each machine)
           -> ferry.send job on the destination machine
ferry.send job -> engine plan.create with the source on the source machine's engine
               -> ferry.send.plan (records the plan, owned by the destination)
               -> engine pull: straight from the source's disk, verified here
```

- **Trust:** the engines pin each other's Ed25519 keys the first time through Park, and every connection after that is mutual TLS 1.3.
- **Access:** the source machine only serves folders under its `[[roots]]`, and only through a grant scoped to the transfer, the destination machine and the items.
- **No relay:** the hub carries no bytes and nothing is staged on a third machine.
- **Requirements:** each machine runs `ferry daemon` (with P2P on) as the same user as `park-node`. The source folder must be inside a `[[roots]]` entry on the source machine.

## Ops

| Op | Effect | What it does |
|---|---|---|
| `ferry.plan` | external | Plan a source folder with the engine (on the owning worker when `machine` is given) and record it |
| `ferry.plan.get` / `.list` | read | One plan with its files / plans, newest first |
| `ferry.source.list` | read | Source folders planned from, and whether each is mounted |
| `ferry.ingest.list` | read | The Ingest view's one read |
| `ferry.offload` | write | Queue a plan's copy as a `ferry.copy` job and record the transfer |
| `ferry.send` | write | Pair the two machines' engines if needed, and queue the `ferry.send` job on the destination machine |
| `ferry.send.plan` | write | Called by the send job: record the engine's plan and attach it to the transfer |
| `ferry.send.fail` | write | Called by the send job when it cannot reach a plan |
| `ferry.transfer.get` / `.list` / `.start` / `.progress` | read / write | Transfer rows and progress |
| `ferry.receipt.record` / `.get` / `.list` / `.file` | write / read | Receipts, and filing verified media into a project |
| `ferry.verify` | external | Re-verify a transfer's receipt with the engine's physical readback; `folder` also reports files the receipt never mentioned |
| `ferry.proxies` | write | Queue a `ferry.proxy` job for the video in a folder |
| `ferry.resolve_project` | write | Record a Resolve project request and queue a `ferry.resolve` job |

Receipts recorded before the engine have no engine receipt. `ferry.verify` re-hashes them through the page cache and says so (`readback_mode: page_cache`).

## Jobs and calls

| Name | Kind | What it does |
|---|---|---|
| `plan` | call | Plan on the worker that can see both folders, with that worker's engine |
| `engine` | call | This machine's engine: `info` (peer id, paired peers), `pair_start`, `pair_complete` |
| `copy` | job | Run a planned offload with the engine; relay progress; register and record the receipt |
| `send` | job | On the destination machine: plan against the source machine's engine, pull, register, record |
| `proxy` | job (`ffmpeg`) | Encode a ProRes proxy per item and register each one |
| `resolve` | job | Write the project manifest and, when Resolve's scripting module is installed, create the project |

## Configuration

The plugin's own file is `~/.ferry/config.toml` (or `./ferry.toml`, or an explicit `config_path`); see [`park_plugins/ferry/config.toml.example`](../park_plugins/ferry/config.toml.example). Unknown keys are refused.

- **`organize.template`:** offloads mirror the source (`{root}/{source_relpath}/{filename}{ext}`). Any other template is refused with a pointer to ferry presets.
- **`organize.on_conflict`:**
  - `skip` skips a file whose full checksum matches and otherwise keeps both.
  - `rename` keeps both, as `name (2).ext`.
  - `overwrite` is refused: Ferry never overwrites.
- **`checksum_algo`:** accepted so an existing config loads. Copies are verified with SHA-256, the hash Park's Assets are keyed by.

The engine has its own, separate `~/.ferry/ferry.toml` (roots, P2P port, agent policy).

## Tests

`tests/park_plugin/` needs Park and builds the `ferry` binary from this checkout. Real daemons run per test: two P2P daemons on loopback stand in for two machines in the send tests. Without Park installed, the tests skip.

```sh
PYTHONPATH=/path/to/park:. /path/to/park/.venv/bin/python -m pytest tests/park_plugin -q
```
