import Foundation

/// A believable day on set, for `FERRY_FIXTURE=1`: two camera cards and a studio drive
/// mounted, two paired machines, one copy running, one agent request waiting, and a few
/// finished copies with receipts. Times are relative to now, so rows read "Today".
@MainActor enum Fixtures {
  /// One plan's route, as `plan.get` and `plan.entries` describe it.
  struct Route {
    let source: String
    let destination: String
    let peer: String
    let root: String
    let files: Int64
    let bytes: Int64
    let actor: String
    let status: String
  }

  static let routes: [String: Route] = [
    "pln_day04": Route(
      source: "A004", destination: "Studio archive", peer: "local",
      root: "/Volumes/Studio/Archive", files: 128, bytes: 48_200_000_000, actor: "human",
      status: "executed"),
    "pln_day03": Route(
      source: "A003", destination: "Studio archive", peer: "local",
      root: "/Volumes/Studio/Archive", files: 96, bytes: 41_700_000_000, actor: "human",
      status: "executed"),
    "pln_b007": Route(
      source: "B007", destination: "Edit suite", peer: "peer_edit", root: "/Volumes/Media/Day 03",
      files: 210, bytes: 88_000_000_000, actor: "human", status: "executed"),
    "pln_sound": Route(
      source: "Sound roll 12", destination: "Field station", peer: "peer_field",
      root: "/Users/field/Sound", files: 64, bytes: 3_100_000_000, actor: "agent:Claude",
      status: "executed"),
    "pln_agent": Route(
      source: "C021", destination: "Studio archive", peer: "local",
      root: "/Volumes/Studio/Archive", files: 42, bytes: 61_400_000_000, actor: "agent:Claude",
      status: "awaiting_approval"),
    "pln_fixture": Route(
      source: "A004", destination: "Studio archive", peer: "local",
      root: "/Volumes/Studio/Archive/Day 04", files: 3, bytes: 48_200_000_000, actor: "human",
      status: "draft"),
  ]

  static func ago(_ seconds: Double) -> JSON {
    .string(ISO8601DateFormatter().string(from: Date().addingTimeInterval(-seconds)))
  }

  static func plan(_ id: String) -> JSON {
    let route = routes[id] ?? routes["pln_agent"]!
    return [
      "plan_id": .string(id), "fingerprint": "8df24f17c9ab", "status": .string(route.status),
      "blocking_count": 0, "actor": .string(route.actor), "checksum_algo": "blake3",
      "total_bytes": .int(route.bytes),
      "counts": id == "pln_fixture"
        ? ["copy": 3, "skip_identical": 1, "dir": 1] : ["copy": .int(route.files)],
      "destinations": [
        [
          "id": "pd_1", "label": .string(route.destination), "peer_id": .string(route.peer),
          "root_path": .string(route.root), "free_bytes": 2_180_000_000_000,
          "required_bytes": .int(route.bytes),
        ]
      ],
    ]
  }

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

  static let receipts: [JSON] = [
    [
      "receipt_id": "rcp_day03", "transfer_id": "tr_day03", "plan_id": "pln_day03",
      "status": "completed", "actor": "human",
      "digest": "c81bc94846fa529188fa7674abef44f926bd0c7f51de431ecc722994aa349806",
      "created_at": ago(26 * 3600),
    ],
    [
      "receipt_id": "rcp_b007", "transfer_id": "tr_b007", "plan_id": "pln_b007",
      "status": "completed", "actor": "human",
      "digest": "4be1a0c2d6f9137e88a35d02c47f61b9e0a7d3c55f218e6b94d07a1c3e8f2b60",
      "created_at": ago(29 * 3600),
    ],
    [
      "receipt_id": "rcp_sound", "transfer_id": "tr_sound", "plan_id": "pln_sound",
      "status": "partial", "actor": "agent:Claude",
      "digest": "9f03c7e1b25a4d68e0f17c3b9a62d5e48c1f07b3a96e2d54c8b1f0a7e3d6c294",
      "created_at": ago(50 * 3600),
    ],
  ]

  static func load(_ s: Store) {
    s.online = true
    s.status = ["name": "Studio Mac", "version": "2.0", "protocol": 1]
    s.drives = [
      [
        "mount_path": "/Volumes/A004", "class": "removable", "free_bytes": 82_000_000_000,
        "card": ["vendor": "Sony"],
      ],
      [
        "mount_path": "/Volumes/B002", "class": "removable", "free_bytes": 58_000_000_000,
        "card": ["vendor": "Canon"],
      ],
      ["mount_path": "/Volumes/Studio", "class": "ssd", "free_bytes": 2_180_000_000_000],
    ]
    s.peers = [
      [
        "id": "peer_edit", "name": "Edit suite", "active_path": ["tier": 2, "badge": "LAN"],
        "last_seen": ago(120),
      ],
      [
        "id": "peer_field", "name": "Field station",
        "active_path": ["tier": 4, "badge": "Tailscale"], "last_seen": ago(3 * 3600),
      ],
    ]
    s.destinations = [
      [
        "id": "archive", "name": "Studio archive", "root_path": "/Volumes/Studio/Archive",
        "resolution": ["state": "available"],
      ],
      [
        "id": "offsite", "name": "Offsite mirror", "root_path": "/Users/field/Mirror",
        "peer_id": "peer_field", "resolution": ["state": "remote"],
      ],
    ]
    s.presets = [
      ["id": "camera", "name": "Camera card conform", "latest_revision": 2],
      ["id": "structure", "name": "Preserve source structure", "latest_revision": 1],
    ]
    s.approvals = [
      "pln_agent": .object(
        plan("pln_agent").fields.merging([
          "reasons": ["Copies over 50 GB need your approval"]
        ]) { _, new in new })
    ]
    s.receipts = receipts
    s.transfers = [
      [
        "id": "tr_day04", "plan_id": "pln_day04", "actor": "human", "status": "running",
        "started_at": ago(7 * 60),
      ],
      [
        "id": "tr_day03", "plan_id": "pln_day03", "actor": "human", "status": "completed",
        "started_at": ago(26 * 3600 + 900), "ended_at": ago(26 * 3600),
      ],
      [
        "id": "tr_b007", "plan_id": "pln_b007", "actor": "human", "status": "completed",
        "started_at": ago(29 * 3600 + 1500), "ended_at": ago(29 * 3600),
      ],
      [
        "id": "tr_sound", "plan_id": "pln_sound", "actor": "agent:Claude", "status": "partial",
        "started_at": ago(50 * 3600 + 400), "ended_at": ago(50 * 3600),
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
    case "plan.create": return plan("pln_fixture")
    case "plan.get": return plan(p["plan_id"].text)
    case "plan.entries":
      let route = routes[p["plan_id"].text] ?? routes["pln_agent"]!
      return [
        "entries": [["source_label": .string(route.source), "source_rel": "DCIM"]],
        "total": .int(route.files),
      ]
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
      let row = receipts.first { $0["receipt_id"] == p["receipt_id"] } ?? receipts[0]
      let route = routes[row["plan_id"].text]!
      let extra: [String: JSON] = [
          "schema": "ferry-receipt-v2", "receipt_digest": row["digest"], "checksum_algo": "blake3",
          "targets": [
            [
              "label": .string(route.destination), "status": row["status"],
              "readback_mode": route.peer == "local" ? "f_nocache" : "remote_readback",
            ]
          ],
          "files": [
            [
              "dest_rel_path": .string("Day 03/\(route.source)_C001.mov"), "action": "copy",
              "readback_mode": "f_nocache", "size": 1_840_000_000,
            ],
            [
              "dest_rel_path": .string("Day 03/\(route.source)_C002.mov"), "action": "copy",
              "readback_mode": "f_nocache", "size": 2_210_000_000,
            ],
          ],
        ]
      return .object(row.fields.merging(extra) { _, new in new })
    case "transfer.get":
      let id = p["transfer_id"].text
      let row = s.transfers.first { $0["id"].text == id } ?? .null
      let route = routes[row["plan_id"].text]
      return [
        "transfer": row, "progress": s.progress[id] ?? .null,
        "targets": [
          [
            "label": .string(route?.destination ?? ""), "status": row["status"],
            "bytes_written": .int(route?.bytes ?? 0), "bytes_verified": .int(route?.bytes ?? 0),
          ]
        ],
        "failures": id == "tr_sound"
          ? [
            ["dest_rel": "Sound/Roll12/T014.wav", "error": "peer went away mid-file"],
            ["dest_rel": "Sound/Roll12/T015.wav", "error": "peer went away mid-file"],
          ] : [],
      ]
    case "doctor.run":
      return [
        ["name": "Daemon", "ok": .bool(true), "detail": "Fixture session, offline sample data"],
        ["name": "Ledger", "ok": .bool(true), "detail": "~/.ferry/ledger.db · integrity ok"],
        ["name": "Paired machines", "ok": .bool(true), "detail": "2 paired · 2 reachable"],
      ]
    default: throw RPCError(code: -1, message: "This action requires a live daemon")
    }
  }
}
