import Foundation
import XCTest

@testable import FerryCore

final class FerryTests: XCTestCase {
  func testWireTypesAndApprovalGate() throws {
    let json: JSON = [
      "plan_id": "p", "status": "draft", "fingerprint": "abc", "counts": ["copy": 2],
      "blocking_count": 0, "total_bytes": 9_007_199_254_740_993,
    ]
    let bytes = try JSONEncoder().encode(json)
    XCTAssertEqual(try JSONDecoder().decode(JSON.self, from: bytes), json)
    let reviewed = try JSONDecoder().decode(ReviewedPlan.self, from: bytes)
    XCTAssertTrue(reviewed.eligible)
    XCTAssertEqual(reviewed.reference, ["plan_id": "p", "fingerprint": "abc"])
    for override: [String: JSON] in [
      ["counts": ["needs_review": 1]], ["status": "invalidated"], ["blocking_count": 1],
      ["fingerprint": ""],
    ] {
      let blocked = JSON.object(json.fields.merging(override) { _, new in new })
      XCTAssertFalse(
        try JSONDecoder().decode(ReviewedPlan.self, from: JSONEncoder().encode(blocked)).eligible)
    }
    XCTAssertEqual(Format.eta(-1), "Estimating")
    XCTAssertEqual(Format.eta(61), "1m 1s")
    XCTAssertFalse(Format.bytes(140000).isEmpty)
  }
  @MainActor func testEventReduction() {
    let store = Store(fixture: false)
    let drive: JSON = ["topic": "drive.mounted", "data": ["mount_path": "/card"]]
    store.reduce(drive)
    store.reduce(drive)
    XCTAssertEqual(store.drives.count, 1)
    store.reduce(["topic": "drive.unmounted", "data": drive["data"]])
    XCTAssertTrue(store.drives.isEmpty)
    store.reduce([
      "topic": "transfer.progress", "data": ["transfer_id": "t", "bytes_verified": 120],
    ])
    XCTAssertEqual(store.progress["t"]?["bytes_verified"], 120)
    store.reduce(["topic": "transfer.finished", "data": ["transfer_id": "t"]])
    XCTAssertTrue(store.progress.isEmpty)
    store.reduce([
      "topic": "plan.awaiting_approval", "data": ["actor": "agent:Claude", "plan_id": "p"],
    ])
    XCTAssertEqual(store.approvals["p"]?["actor"], "agent:Claude")
  }

  func testScratchDaemonTransferAndReadback() async throws {
    let fm = FileManager.default
    let root = URL(fileURLWithPath: "/private/tmp").appendingPathComponent(
      "ferry-test-" + UUID().uuidString)
    try fm.createDirectory(
      at: root, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
    defer { try? fm.removeItem(at: root) }
    let repo = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
      .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
    let build = Process()
    build.currentDirectoryURL = repo
    build.executableURL = URL(fileURLWithPath: "/usr/bin/make")
    build.arguments = ["build"]
    try build.run()
    build.waitUntilExit()
    XCTAssertEqual(build.terminationStatus, 0)
    let src = root.appendingPathComponent("source")
    let dest = root.appendingPathComponent("destination")
    for dir in [src, dest] { try fm.createDirectory(at: dir, withIntermediateDirectories: false) }
    try Data("camera original".utf8).write(to: src.appendingPathComponent("clip.txt"))
    let binary = repo.appendingPathComponent("bin/ferry")
    let studio = try await ScratchDaemon(
      binary: binary, home: root.appendingPathComponent("studio"), export: src, access: "ro")
    defer { studio.stop() }
    let backup = root.appendingPathComponent("backup")
    try fm.createDirectory(at: backup, withIntermediateDirectories: false)
    let archive = try await ScratchDaemon(
      binary: binary, home: root.appendingPathComponent("archive"), export: backup)
    defer { archive.stop() }
    let client = studio.client
    let invite = try await archive.client.commands.call("peer.pair.start", ["ttl_seconds": 60])
    XCTAssertTrue(invite["code"].text.hasPrefix("ferry://pair/"))
    let peer = try await client.commands.call("peer.pair.complete", ["code": invite["code"]])
    let routes = try await client.commands.call("peer.probe", ["peer_id": peer["id"]])
    XCTAssertFalse(routes["measurements"].rows.isEmpty)
    let subscription = try await client.subscribe()
    defer { subscription.close() }
    let delivered = expectation(description: "Transfer finished event")
    let reader = Task {
      for await event in subscription.events where event["topic"] == "transfer.finished" {
        delivered.fulfill()
        break
      }
    }
    defer { reader.cancel() }
    async let status = client.commands.call("daemon.status")
    async let presets = client.commands.call("preset.list")
    let (s, p) = try await (status, presets)
    XCTAssertEqual(s["protocol"], 1)
    XCTAssertFalse(p.rows.isEmpty)
    do {
      _ = try await client.commands.call("plan.get", ["oops": "p"])
      XCTFail("Accepted an unknown parameter")
    } catch let error as RPCError { XCTAssertEqual(error.code, -32602) }
    let store = await Store(fixture: false, client: client)
    let params = try await MainActor.run {
      store.sources = [Location(path: src.path)]
      store.targets = [
        Location(path: dest.path), Location(path: backup.path, peerID: peer["id"].text),
      ]
      store.checksum = "blake3"
      return try PlanRequest(workspace: store).params
    }
    let plan = try await client.commands.call("plan.create", params)
    let planDest = try XCTUnwrap(plan["destinations"].rows.first)
    let tree = try await client.commands.call(
      "plan.tree", ["plan_id": plan["plan_id"], "destination_id": planDest["id"]])
    XCTAssertEqual(tree["file_count"], 1)
    let successor = try await client.commands.call(
      "plan.resolve",
      ["plan_id": plan["plan_id"], "revision": ["conflict_policy": "skip_identical"]])
    let reviewed = try JSONDecoder().decode(
      ReviewedPlan.self, from: JSONEncoder().encode(successor))
    try await store.approve(reviewed)
    await fulfillment(of: [delivered], timeout: 20)
    let receipts = try await client.commands.call("receipt.list")
    let receipt = try XCTUnwrap(receipts.rows.first)
    let ref: JSON = ["receipt_id": receipt["receipt_id"]]
    let doc = try await client.commands.call("receipt.get", ref)
    XCTAssertEqual(doc["status"], "completed", doc["files"].pretty)
    XCTAssertEqual(doc["schema"], "ferry-receipt-v2")
    XCTAssertTrue(doc["targets"].rows.contains { $0["readback_mode"] == "remote_readback" })
    let verified = try await client.commands.call("verify.run", ref)
    XCTAssertEqual(verified["clean"], .bool(true))
    XCTAssertEqual(verified["checked"], 2)
    let file = try XCTUnwrap(doc["files"].rows.first(where: { $0["action"] == "copied" }))
    try Data("modified original".utf8).write(
      to: dest.appendingPathComponent(file["dest_rel_path"].text))
    let changed = try await client.commands.call("verify.run", ref)
    XCTAssertEqual(changed["modified"], 1)

    let waiting = root.appendingPathComponent("awaiting")
    try fm.createDirectory(at: waiting, withIntermediateDirectories: false)
    let agent = try await client.commands.call(
      "plan.create",
      [
        "sources": [["path": .string(src.path)]], "destinations": [["path": .string(waiting.path)]],
        "actor": "agent:Claude",
      ])
    let request: JSON = [
      "plan_id": agent["plan_id"], "fingerprint": agent["fingerprint"], "actor": "agent:Claude",
    ]
    let held = try await client.commands.call("transfer.start", request)
    XCTAssertEqual(held["status"], "awaiting_approval")
    try await store.refresh()
    let pending = await store.approvals[agent["plan_id"].text]
    let approved = try XCTUnwrap(pending)
    try await store.approve(
      JSONDecoder().decode(ReviewedPlan.self, from: JSONEncoder().encode(approved)))
    var agentReceipt: JSON?
    for _ in 0..<150 {
      let list = try await client.commands.call("receipt.list")
      agentReceipt = list.rows.first { $0["plan_id"] == agent["plan_id"] }
      if agentReceipt != nil { break }
      try await Task.sleep(for: .milliseconds(40))
    }
    XCTAssertEqual(agentReceipt?["actor"], "agent:Claude")
    XCTAssertEqual(agentReceipt?["status"], "completed")

  }
}

private struct ScratchDaemon {
  let process: Process
  let client: DaemonClient
  init(binary: URL, home: URL, export: URL? = nil, access: String = "rw") async throws {
    let fm = FileManager.default
    let prefs = home.appendingPathComponent(".ferry")
    try fm.createDirectory(
      at: prefs, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
    if let export {
      try "[[roots]]\npath = \"\(export.path)\"\naccess = \"\(access)\"\n".write(
        to: prefs.appendingPathComponent("ferry.toml"), atomically: true, encoding: .utf8)
    }
    let socket = prefs.appendingPathComponent("ferry.sock").path
    client = DaemonClient(path: socket)
    process = Process()
    process.executableURL = binary
    process.arguments = ["daemon", "--socket", socket, "--listen", "127.0.0.1:0", "--no-mdns"]
    process.environment = ["HOME": home.path, "PATH": "/usr/bin:/usr/sbin:/bin:/sbin"]
    try process.run()
    for _ in 0..<200 where !fm.fileExists(atPath: socket) && process.isRunning {
      try await Task.sleep(for: .milliseconds(25))
    }
  }
  func stop() {
    client.commands.close()
    if process.isRunning {
      process.terminate()
      process.waitUntilExit()
    }
  }
}
