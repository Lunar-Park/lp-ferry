import Foundation

@MainActor enum Fixtures {
  static let target: JSON = [
    "id": "pd_1", "label": "Studio archive", "root_path": "/Volumes/Studio/Day 04",
    "free_bytes": 2_180_000_000_000, "required_bytes": 48_200_000_000,
  ]
  static let plan: JSON = [
    "plan_id": "pln_fixture", "fingerprint": "8df24f17c9ab", "status": "draft", "blocking_count": 0,
    "actor": "human", "checksum_algo": "blake3", "total_bytes": 48_200_000_000,
    "counts": ["copy": 3, "skip_identical": 1, "dir": 1], "destinations": .array([target]),
  ]
  static let tree: JSON = [
    "children": [
      [
        "kind": "file", "name": "A004_C018.mov", "path": "Day 04/A004_C018.mov",
        "before": "A004:DCIM/C018.mov", "action": "copy", "renamed_count": 1,
        "total_bytes": 18_200_000_000,
      ],
      [
        "kind": "file", "name": "A004_C019.mov", "path": "Day 04/A004_C019.mov",
        "before": "A004:DCIM/C019.mov", "action": "copy", "renamed_count": 1,
        "total_bytes": 14_000_000_000,
      ],
      [
        "kind": "file", "name": "C020.mov", "path": "Day 04/C020.mov",
        "before": "A004:DCIM/C020.mov", "action": "copy", "total_bytes": 16_000_000_000,
      ],
      [
        "kind": "file", "name": "index.xml", "path": "Day 04/index.xml",
        "before": "A004:DCIM/index.xml", "action": "skip_identical", "total_bytes": 2048,
      ],
    ]
  ]
  static let receipt: JSON = [
    "receipt_id": "rcp_day03", "transfer_id": "tr_day03", "status": "completed", "actor": "human",
    "digest": "c81bc94846fa529188fa7674abef44f926bd0c7f51de431ecc722994aa349806",
    "created_at": "2026-11-22T19:24:00Z",
  ]
  static func load(_ s: Store) {
    s.online = true
    s.status = ["name": "Studio Mac", "version": "2.0", "protocol": 1]
    s.drives = [
      [
        "mount_path": "/Volumes/A004", "class": "removable", "free_bytes": 82_000_000_000,
        "card": ["vendor": "Sony"],
      ], ["mount_path": "/Volumes/Studio", "class": "ssd", "free_bytes": 2_180_000_000_000],
    ]
    s.peers = [
      [
        "id": "peer_edit", "name": "Edit suite", "active_path": ["tier": 2, "badge": "LAN"],
        "last_seen": "2026-11-23",
      ],
      [
        "id": "peer_field", "name": "Field station",
        "active_path": ["tier": 4, "badge": "Tailscale"],
      ],
    ]
    s.destinations = [
      [
        "id": "archive", "name": "Studio archive", "root_path": "/Volumes/Studio",
        "resolution": ["state": "available"],
      ]
    ]
    s.presets = [
      ["id": "camera", "name": "Camera card conform", "latest_revision": 2],
      ["id": "structure", "name": "Preserve source structure", "latest_revision": 1],
    ]
    s.sources = [Location(path: "/Volumes/A004")]
    s.targets = [Location(destinationID: "archive", label: "Studio archive")]
    s.approvals = [
      "pln_agent": [
        "plan_id": "pln_agent", "actor": "agent:Claude",
        "reasons": ["Destination requires human approval"],
      ]
    ]
    s.receipts = [receipt]
    s.transfers = [
      [
        "id": "tr_day04", "actor": "human", "status": "running",
        "started_at": "2026-11-23T19:15:00Z",
      ],
      [
        "id": "tr_day03", "actor": "human", "status": "completed",
        "started_at": "2026-11-22T19:23:00Z",
      ],
    ]
    s.progress = [
      "tr_day04": [
        "transfer_id": "tr_day04", "overall_fraction": .number(0.58), "eta_seconds": 78,
        "targets": [
          [
            "label": "Studio archive", "transport_badge": "Local SSD",
            "current_file": "DCIM/A004_C018.mov", "bytes_written": 36_500_000_000,
            "bytes_verified": 19_800_000_000, "total_bytes": 48_200_000_000,
            "write_bps": 312_000_000, "verify_bps": 285_000_000, "done_files": 74,
            "total_files": 128,
          ]
        ],
      ]
    ]
  }
  static func reply(_ method: String, _ p: JSON, _ s: Store) throws -> JSON {
    switch method {
    case "plan.create": return plan
    case "plan.get":
      return .object(
        plan.fields.merging([
          "plan_id": p["plan_id"], "actor": "agent:Claude", "status": "awaiting_approval",
        ]) { _, new in new })
    case "plan.tree":
      if p["parent_path"] == "Day 04" { return tree }
      return [
        "children": [
          [
            "kind": "dir", "name": "Day 04", "path": "Day 04", "file_count": 4,
            "total_bytes": 48_200_002_048,
          ]
        ]
      ]
    case "receipt.get":
      return .object(
        receipt.fields.merging([
          "schema": "ferry-receipt-v2", "receipt_digest": receipt["digest"],
          "checksum_algo": "blake3",
          "targets": [
            ["label": "Studio archive", "status": "completed", "readback_mode": "physical"]
          ],
        ]) { _, new in new })
    case "preset.preview":
      return .array(
        p["paths"].rows.map { rel in
          let output =
            p["id"] == "structure"
            ? rel.text
            : p["source_label"].text + "/" + URL(fileURLWithPath: rel.text).lastPathComponent
          return ["rel": rel, "dest_rel": .string(output), "action": "copy"]
        })
    case "preset.get":
      return [
        "preset": [
          "schema": "ferry-preset-v1",
          "name": s.presets.first(where: { $0["id"] == p["id"] })?["name"] ?? "Untitled",
          "fallback": p["id"] == "structure"
            ? "{relative_dir}/{filename}" : "{source_label}/{filename}", "rules": [],
        ]
      ]
    case "transfer.get": return ["progress": s.progress[p["transfer_id"].text] ?? .null]
    case "doctor.run":
      return [["name": "Fixture session", "ok": .bool(true), "detail": "Offline sample data"]]
    default: throw RPCError(code: -1, message: "This action requires a live daemon")
    }
  }
}
