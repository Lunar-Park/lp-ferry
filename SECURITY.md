# Security and secret handling

## Reporting a vulnerability

**Report privately. Do not open a public issue for a security problem.**

ferry handles local media paths, file contents, and an audit database. If you
have found something that lets one local user read or write another user's
data, escape a sandbox boundary, or execute code through a crafted media file
or project, report it to the maintainer directly at **security@dspury.com**.

Please include: what you found, how to reproduce it, the affected version
(`ferry version --json`), and your platform. You should get an acknowledgement within a few days. If you need
the report to stay quiet while it is investigated, say so and it will.

Note that the maintainer is a single person, so a realistic response time is
"eventually" rather than "within 24 hours" for non-critical reports. Reports
of credential exposure or data loss are treated as urgent.

Once a fix is ready, please keep the details private until it is released so
that users have a version to upgrade to. Credit will be given in the release
notes unless you would rather not be named.

Everything else — bugs, packaging questions, feature requests — belongs in a
public issue or a discussion. This section is only for things that should not
be public yet.

## Threat model

Ferry is one binary. `ferry daemon` owns all disk and network work; the CLI, `ferry mcp` and the
apps reach it only through a local socket. Its exposure comes from three surfaces.

**The control socket** (`~/.ferry/ferry.sock`) is mode `0600` inside a `0700` directory, and the
daemon also checks that the connecting process belongs to the same OS user. Anything running as
that user can drive Ferry. The `actor` a client declares (`human` or `agent:<name>`) is
attribution and a policy hook, not authentication between processes of the same user.

**The P2P listener** (default `:9410` on every interface, IPv4 and IPv6; `ferry daemon --listen
ADDR`, repeatable, binds only the named addresses, and `--no-p2p` turns it off) speaks only
mutual TLS 1.3, with no certificate authority:

- **Unpaired keys are refused at the handshake.** A connection completes only if both sides'
  Ed25519 keys are pinned, and that is re-checked on every connection: session resumption is
  off.
- **Pairing proves a code over the connection itself.** Pairing needs a single-use, 128-bit code
  that is valid for a few minutes. Both machines prove they know it with an HMAC over the TLS
  channel binding, so a man in the middle cannot complete it. Treat an open pairing code like a
  password.
- **Every file read needs a grant.** The source machine signs the grant, and it binds one
  transfer, one receiving machine, an explicit list of items and an expiry. A paired peer cannot
  fetch a file by name or hash without one. Clients never follow redirects, so a grant never
  leaves the machine it was issued to.
- **Only configured `[[roots]]` are reachable.** Nothing outside them is served or written, and
  writes need an `rw` root. Paths are checked one component at a time without following
  symlinks.

**Accepted limits.** These are known, current choices:

- **Pairing is trust.** A paired machine may request grants for anything under the roots you
  export to it. Pair only machines you would hand those folders to, and unpin a machine with
  `ferry peer remove`.
- **The identity key is the machine's identity.** `~/.ferry/identity.key` (`0600`) is that
  machine. Anyone who can read it can impersonate it to every peer that pinned it.
- **mDNS is visible on the local network.** Bonjour advertising (on by default) announces the
  peer ID and its addresses to that network. Run `ferry daemon --no-mdns` where that matters.
- **There is no rate limiting.** The listener has no rate limiting or denial-of-service hardening,
  so do not expose it to the public internet. A private LAN, a direct link or a mesh VPN is the
  intended deployment.

**Media parsing** reads container headers (MP4/MOV, EXIF, WAV/BWF, Sony XML) in pure Go, with
bounded walks and no frame decoding. A crafted media file that crashes or hangs the daemon is in
scope.

**The Park plugin** no longer uses Park's node file server: `ferry.send` goes engine to engine
over the transport above. If you run a node file server for other reasons, the caveat recorded
for it still applies. It accepts any valid hub token over plain HTTP, so keep it on a trusted
network.

Reports we want: reading a file without a valid grant, pairing or connecting without a pinned key,
escaping a root or a landing folder, writing into a read-only root or a source, overwriting an
existing file, and a published file that does not match its receipt.

## Secret scanning

Live credentials, private keys, bearer values, recovery material, and
secret-bearing production exports do not belong in this repository, whether it
is private or public.

Install Gitleaks and run:

```bash
./scripts/secret-scan.sh staged
./scripts/secret-scan.sh all
```

Enable the versioned local pre-commit hook with:

```bash
git config core.hooksPath .githooks
```

The hook fails closed when Gitleaks is unavailable. CI downloads the pinned
Gitleaks release, verifies its upstream checksum, and scans both the working
tree and complete fetched history with read-only repository permissions.

Do not bypass a failed scan. If a finding is real, revoke or rotate it first,
then remove the material and rescan. Do not copy a credential into an issue,
pull request, log, or remediation note. Any allowlist exception must be narrow
to the exact rule, path, and proven synthetic or false-positive value.

Before making a private repository public, also review every branch and tag,
Git LFS and submodule content, release and Actions artifacts, generated
archives, source maps, notebooks, fixtures, logs, screenshots, workflows, and
private topology. Repeat the scan from a fresh clone and obtain repository-owner
approval before changing visibility.
