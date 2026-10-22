# D-2 gate 5 — evidence

Raw output from the D-2 gate 5 sustained-transfer run (2026-10-21/22), kept
with the record in [`../../D2-storage-matrix.md`](../../D2-storage-matrix.md).
§12.2 asks for the timeline, not a single number, and for the independent walk
to stand on its own; these files are the evidence behind that section.

| file | opaque ref | what it is |
| --- | --- | --- |
| `timeline.jsonl` | ARTIFACT-1 | the collector's sustained-run series: 403 samples at 30 s over 5.48 h (2026-10-21T19:24:15Z → 2026-10-22T00:52:47Z). One JSON object per sample: `at`, `app_rss_bytes`, `sidecar_rss_bytes`, and the state of every transfer execution the collector saw. |
| `campaign-results.json` | ARTIFACT-2 | per-plan results of the chunked campaign: `plan`, `state`, `plan_id`, `files`, `bytes`, `minutes`, and how many chunk roots each plan covered. |
| `verify-receipt.json` | ARTIFACT-3 | `--verify-receipt` over all 11 receipts: `hash_matches`, `export_matches`, `committed_entries`, `entries_with_matching_checksums`, `entries_missing_or_mismatched_checksums`. |
| `verify-destination.json` | ARTIFACT-4 | `--verify-destination` over all 11 receipts: per-receipt `verified-identical` tally, `directories`, `extra_count`, `failures`. |

## Two files are redacted copies

`campaign-results.json` and `verify-destination.json` are **redacted** copies
of the tool output, and this is deliberate:

- `campaign-results.json` replaces each plan's `paths` array (the private
  source directory names) with the count of chunk roots it covered.
- `verify-destination.json` replaces each receipt's `extra` array (a 50-entry
  sample of private destination paths) with the length of that sample.

Both files were rewritten by dropping those string arrays and nothing else.
Every number is byte-for-byte the tool's own output — verified by re-reading
each redacted file against the original — so the tallies in the record can be
recomputed from what is committed here.

The reason is §12.2's leak rule: keep private addresses and filenames out of
committed artifacts, and paste no directory trees or mount paths. This
repository is **public**, and the source was a real backup drive, so the
unredacted arrays would have published private path names. The matrix's own
gate-5 record is written to the same rule — the source and destination appear
as opaque labels, not as volume names.

The unredacted originals are kept outside the repository, at
`/Users/selene/Lunar-Park/file-ferry-artifacts/d2-gate5/`, with these
sha256 sums:

| file | sha256 |
| --- | --- |
| `timeline.jsonl` | `02fe12bd6c40f8aa1813ea2dffd7715fe7262c5637396c626bba51006d0aa6d2` |
| `campaign-results.json` | `d877da3af516f4e3c71bff5c949bd17cfe8bedcb02379df6c60398d40f8e3170` |
| `verify-receipt.json` | `4bb05c66218598d25e4cef323ef525ac7001c71cf32173d07188337c3c75d762` |
| `verify-destination.json` | `308a550e4496392be560cad4fab6a883d133edd607b5ae7d73eff131be3569a7` |

`timeline.jsonl` and `verify-receipt.json` are committed verbatim — the
collector writes no path names, and the receipt report is counts only.

## Reading the timeline

One sample per line. `executions` is the state of each transfer execution at
that moment: `state`, `items_committed`, `bytes_committed`, `updated_at`.
The committed counters are monotonic, with one deliberate exception: the
execution the interruption landed in holds its last value while its
`updated_at` stays frozen and its state still reads `running` — that frozen
pair is how the orphaned job was diagnosed from the database rather than from
the process table. The sidecar pid changes once, at the resume.
