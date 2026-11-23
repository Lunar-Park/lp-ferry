# Contributing to ferry

Thanks for considering it. ferry is a verified, local-first file transport. It's one Go binary, providing a daemon, a CLI and an MCP server, plus a thin Park plugin. Start with the [README](README.md), then [docs/GO-QUICKSTART.md](docs/GO-QUICKSTART.md).

## What state the project is in

Ferry v2 replaced the Python CLI/TUI and the Electron app; that code is preserved at the `legacy-python-v0.3` tag. The engine's safety guarantees each have a failure-injection test, and they all run under `go test -race`:
- no-replace publication
- physical readback
- symlink and unmounted-share refusal
- crash-window recovery
- cancellation
- grant and TLS boundaries

**What has not yet been proven on real hardware:**
- A transfer between two physical machines; the network tests run on loopback.
- A long soak, from SD card to NVMe and a 10GbE server at once.

Do not read a green suite as that proof.

## Getting set up

You need Go 1.26+ and `golangci-lint`. For the Park plugin you also need Python 3.11+ and `ruff`.

```sh
git config core.hooksPath .githooks   # the pre-commit hook runs the secret scan
make build                            # bin/ferry
```

## The gates

CI runs exactly these. Run them before you open a pull request:

```sh
make vet lint race cross              # Go: vet, golangci-lint, race tests, static cross-builds
ruff check park_plugins tests
ruff format --check park_plugins tests
PYTHONPATH=/path/to/park:. python -m pytest tests/park_plugin   # needs Park; builds bin/ferry itself
./scripts/secret-scan.sh all          # working tree and full history
```

Run lint for both operating systems you touch: `GOOS=linux golangci-lint run` catches stat-field widths that differ from darwin.

**The secret scan is not optional and is not to be bypassed.** If it finds something real, revoke or rotate the credential *first*, then remove it and rescan. Do not paste a credential into an issue, a pull request, a log, or a comment explaining a fix. See [`SECURITY.md`](./SECURITY.md).

**The repository is public.** Never commit private hostnames, personal NAS paths, credentials or client media paths, including in tests and fixtures.

## Tests

Add tests with the code, not after.

- **Never mock the boundary you are testing.** Use real temp directories, real SQLite WAL files, real Ed25519 / mutual TLS 1.3 over loopback, and real daemons. Failure injection is fine: a hook that returns ENOSPC, a proxy that cuts a connection mid-file. Faking the filesystem or the crypto is not.
- **Ask what caller reaches the code.** A green suite has covered unreachable code here before.
- **A check that cannot fail is worse than no check.** If you write a guard, break it and watch it go red before you believe it.
- **Pin the plan's test IDs.** The phase tests (T1.x–T4.x) are named after the [rewrite plan](docs/GO-BACKEND-REWRITE-PLAN.md) and pin its exit gates. Keep their names.

## Pull requests

- Branch from `main`. Do not stack branches.
- One piece of work per pull request, and say what it is in the title.
- Describe what you verified and how, not just what you changed.
- If something in your change cannot be verified in this environment, say so explicitly and name what is unproven.
- Update the docs your change makes wrong. `docs/IPC-PROTOCOL.md` is a contract that apps bind to: change it in the same pull request as the code.
- If a gate fails, fix the gate or explain it. Do not weaken a check to make a build green.

Do not open a pull request from an agent that has not been told the work is finished. Do not push, merge, tag or publish on your own initiative; say what you would like to happen and let a human decide.

## Reporting bugs

Open an issue with the `.github` bug template. What helps most:

- `ferry version --json`, your OS, and how you installed.
- The command you ran and what happened instead.
- The receipt (`ferry receipt show <id>`) and `ferry doctor` output. Receipts are the fastest route to a real diagnosis.

`SECURITY.md` covers how to report a security issue privately. Please do not open a public issue for one.

## Licence

MIT; see [`LICENSE`](./LICENSE). By contributing you agree your work is published under it.
