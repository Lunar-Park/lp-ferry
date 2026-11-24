# Release

Ferry ships as one static binary per platform: `linux/amd64`, `linux/arm64`, `darwin/arm64` and `darwin/amd64`. It is built with `CGO_ENABLED=0`, so it needs no C toolchain on the build machine or at runtime.

## Build a release

Go releases are tagged `v2.x.y`. Older `v0.*` tags and `legacy-python-v0.3` belong to the retired Python app; [GoReleaser](../.goreleaser.yaml) ignores them.

```sh
goreleaser check                         # validates .goreleaser.yaml (CI runs this too)
goreleaser release --snapshot --clean    # local dry run: dist/ferry_2.0.0-dev+<commit>_<os>_<arch>.tar.gz
git tag v2.0.0 && goreleaser release     # a real (draft) release, once a maintainer decides to publish
```

Before archiving, the release runs `go test -race` over everything. Each archive holds the binary, `LICENSE`, `SECURITY.md`, the quickstart, the IPC protocol, the example config and both service files. `checksums.txt` lists the SHA-256 of every archive. Releases are created as drafts; a maintainer publishes them.

For a quick local build without GoReleaser, `make cross` builds the same four binaries into `bin/`.

## Where things live

| What | macOS | Linux |
|---|---|---|
| Daemon config | `~/.ferry/ferry.toml` | `~/.ferry/ferry.toml` |
| Control socket (0600) | `~/.ferry/ferry.sock` | `~/.ferry/ferry.sock` |
| Identity key (0600) | `~/.ferry/identity.key` | `~/.ferry/identity.key` |
| Ledger (SQLite, WAL) | `~/Library/Application Support/ferry/ferry.db` | `~/.local/share/ferry/ferry.db` |
| Receipts | `~/.ferry/receipts/<receipt_id>.json` | `~/.ferry/receipts/<receipt_id>.json` |
| Park plugin config | `~/.ferry/config.toml` | `~/.ferry/config.toml` |

`FERRY_DB` overrides the ledger path, and the `[daemon]` keys override the socket, ledger and receipts paths. Under the systemd unit, `~` is `/var/lib/ferry`.

The identity key is the machine's Ed25519 identity, which paired machines have pinned. Back it up with the ledger. Losing it means pairing again; copying it to another machine would let that machine impersonate this one.

## Signing and notarization

Not done yet. The macOS binary is unsigned. Embedding it in the signed SwiftUI app bundle is the intended route, and the static build makes that a plain copy.

## Operational policy

- **Upgrades:** stop the daemon, replace the binary, then start it again. The ledger migrates forward on open; migrations are never edited once shipped.
- **Unfinished transfers:** a transfer that was running when the daemon stopped becomes `needs_attention` at the next start. `ferry resume <id>` reconciles it without re-copying verified files.
- **Health:** `ferry doctor` checks the setup, with or without the daemon running.
