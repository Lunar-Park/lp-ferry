# Sending between machines

Ferry's job is to make moving media reliable and repeatable **for agents**: a card to an SSD, an
SSD to another SSD, an SSD to another machine. An agent asks for a move; Ferry hashes at both
ends, so the agent can prove that every byte arrived and nothing was lost or altered in transit,
and the answer is a receipt it can re-check later. "Secure" and "repeatable" are requirements, not
extras: a run can be repeated safely, and no footage moves on a weaker path than the one the agent
can verify.

`ferry.send` is the machine-to-machine half of that. Its one hard rule, decided up front: **bytes
go directly from the source machine to the destination machine and are never staged on a third
machine.** The Park hub introduces the two machines to each other and keeps the receipt. It does
not carry the bytes.

```
machine A (source)                hub                machine B (destination)
  ferry.stage  --------------->  plan, receipt  <---------------  ferry.land
  hashes in place                names both               pulls each file by hash
  copies nothing                 machines                 straight from A's file server
```

## What a send guarantees

- **Every file hashed twice.** Once on the source, in place, before anything moves; once again on
  arrival, from the bytes that landed. A file that arrives with another hash is deleted and
  reported, never counted as delivered.
- **A receipt naming both machines.** It is an ordinary copy receipt whose `detail.send` names the
  source and destination machine, so `ferry.verify` and the Ingest view read it with no special
  case.
- **Existing destinations are preserved.** A file already at the destination is hashed and
  accepted only when it matches; a newly appearing destination is never replaced. `overwrite` is
  refused for a send.
- **The source is never deleted or written to.** `ferry.stage` hashes and registers where the
  files already are and declares `egress = "none"`. Moving the source is a separate decision,
  after a verified receipt, and is not part of a send.
- **Nothing is staged in between.** If the destination cannot reach the source, the send fails with
  the reason; it never falls back to copying through the hub.

## Requirements

1. **Park's direct fetch.** `ferry.land` uses `fetch_asset(..., from_machine=)` to take the bytes
   from the source machine's node file server. A Park without it cannot land a send.
2. **A file server on the source machine.** In that machine's `node.toml`, `serve_files = true`,
   a `bind` address the destination can reach, and an `advertise_url` the hub hands out. Enable it
   only on a network you control — see *Network exposure* below.
3. **Ferry loaded on both nodes**, and both machines registered with the hub.
4. **A destination that can reach the source.** Direct is expected on the same LAN or mesh VPN; a
   send across two networks with no shared path is not supported.

A first start of a node that serves files asks the OS whether it may accept incoming connections.
Someone has to answer that prompt, or the first send times out with no obvious cause.

`ferry.send` refuses a source and destination on the same machine; that case is
`ferry.offload`, which copies between two folders on one machine.

## Running one

```python
# queues the stage job on the source machine
send = ferry.send(
    source_machine="studio-a",
    source="/Volumes/CARD_A/CARD_A",
    destination_machine="studio-b",
    destination="/Volumes/WORK/2026-11-19_client",
    label="2026-11-19 client card",
)

# when the stage job finishes it calls this itself on the destination machine
ferry.send.land(transfer=send["transfer"], ...)

# then, whenever you want to re-prove the copy
ferry.verify(transfer=send["transfer"])
```

| Op | Who calls it | What it does |
|---|---|---|
| `ferry.send` | you or an agent | Queues the stage job on the source machine; returns `{transfer, job, source_machine, destination_machine, queued}` |
| `ferry.stage` | the source machine's node | Hashes every file in place and registers it; copies nothing |
| `ferry.send.land` | the stage job | Records the landing plan and queues the land job on the destination machine |
| `ferry.land` | the destination machine's node | Fetches each file by hash from the source, re-hashes it, registers it, writes the receipt |
| `ferry.send.fail` | the stage job | Ends the transfer with the reason when it cannot reach a plan |
| `ferry.verify` | you or an agent | Re-hashes the destinations against the receipt |

## Network exposure — read before you enable a file server

Today a node's file server accepts **any valid hub token**: whoever holds one, plus a SHA-256, can
read that file from the server. Traffic is **plain HTTP**; there is no TLS and no per-transfer
grant.

The intended deployment is a mesh VPN or a private LAN where every peer holding a node token is
already trusted with that machine's media. Do not expose a node file server to an untrusted
network or to the public internet. A grant scoped to a single transfer, issued by the hub and
expiring with it, is the change that would lift this limit; it is not built.

See `SECURITY.md` for how to report a problem with any of this.

## Testing

The send is covered by `tests/park_plugin/test_send.py`, which needs Park importable. Those tests
**skip when Park is not installed**, which is the normal case for this repository's CI: Ferry runs
standalone, and Park is a separate host. A green CI run therefore does not exercise the send; run
the suite with Park on `PYTHONPATH` to prove it:

```bash
PYTHONPATH=/path/to/park:. pytest tests/park_plugin -q
```

## Status and what is not built

- **Proven:** the send's own logic, in one process and over a real file server on localhost;
  hashing and receipts on both sides; the guards that refuse an overwrite, a path escape, and a
  write into the source (three mutation checks fail the suite when a guard is removed).
- **Proven separately:** the network path — a node file server answering a real pull between two
  machines, hash matching on arrival, `401` with a missing or wrong token, byte ranges honoured.
- **Not yet:** a send between two real machines end to end, a check that the arrival hash reads
  the bytes back from the physical disk rather than the OS cache, a fsync before the arrival hash,
  a destination pre-check (free space, writable, expected mount) before the source is hashed, a
  grant scoped to one transfer, TLS, and Windows destinations.
