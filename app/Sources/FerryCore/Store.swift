import Foundation
import Network
import Observation

@MainActor @Observable package final class Store {
  package let fixture: Bool
  private let client: DaemonClient
  private var child: Process?
  private var started = false
  package var status: JSON = .null
  package var online = false
  package var connectionNote = "Connecting"
  package var error: String?
  package var drives = [JSON]()
  package var peers = [JSON]()
  package var destinations = [JSON]()
  package var presets = [JSON]()
  package var transfers = [JSON]()
  package var receipts = [JSON]()
  package var progress = [String: JSON]()
  package var approvals = [String: JSON]()
  package var onApproval: ((JSON) -> Void)?
  package var sources = [Location]()
  package var targets = [Location]()
  package var section = "Transfer"
  package var preset = ""
  package var policy = "keep_both"
  package var checksum = "sha256"
  package var roles = [String: String]()
  package var review: JSON?
  package init(
    fixture: Bool = ProcessInfo.processInfo.environment["FERRY_FIXTURE"] == "1",
    client: DaemonClient = .init()
  ) {
    self.fixture = fixture
    self.client = client
    if fixture { Fixtures.load(self) }
  }
  package func rpc(_ method: String, _ params: JSON = [:]) async throws -> JSON {
    if fixture { return try Fixtures.reply(method, params, self) }
    return try await client.commands.call(method, params)
  }
  package func perform(_ work: @escaping @MainActor () async throws -> Void) {
    Task {
      do { try await work() } catch is CancellationError {} catch {
        self.error = error.localizedDescription
      }
    }
  }
  package func start() {
    guard !fixture, !started else { return }
    started = true
    Task {
      while !Task.isCancelled {
        do {
          let subscription = try await client.subscribe()
          defer { subscription.close() }
          try await refresh()
          online = true
          for await event in subscription.events {
            reduce(event)
            if event["topic"].text != "transfer.progress" { try await refresh() }
          }
        } catch {
          connectionNote = error.localizedDescription
          if let e = error as? NWError, case .posix(let code) = e,
            [.ENOENT, .ECONNREFUSED].contains(code), child?.isRunning != true
          {
            do { child = try client.launchBundledDaemon() } catch {
              connectionNote = error.localizedDescription
            }
          }
        }
        online = false
        try? await Task.sleep(for: .seconds(2))
      }
    }
  }
  package func refresh() async throws {
    if fixture { return }
    let s = try await rpc("daemon.status")
    guard s["protocol"] == 1 else {
      throw RPCError(code: -1, message: "Unsupported Ferry protocol")
    }
    status = s
    drives = try await rpc("drive.list")["drives"].rows
    peers = try await rpc("peer.list").rows.filter { $0["revoked"] != .bool(true) }
    destinations = try await rpc("dest.list").rows
    presets = try await rpc("preset.list").rows
    transfers = try await rpc("transfer.list").rows
    receipts = try await rpc("receipt.list").rows
    var current = [String: JSON]()
    for item in s["active_transfers"].rows {
      let id = item["transfer_id"].text
      let detail = try await rpc("transfer.get", ["transfer_id": .string(id)])
      current[id] = detail["progress"] == .null ? item : detail["progress"]
    }
    progress = current
    var waiting = [String: JSON]()
    for id in s["awaiting_approval"].rows {
      let plan = try await rpc("plan.get", ["plan_id": id])
      if plan["status"] == "awaiting_approval" {
        waiting[id.text] = .object(
          plan.fields.merging(["reasons": approvals[id.text]?["reasons"] ?? .null]) { _, new in new
          })
      }
    }
    approvals = waiting
  }
  package func reduce(_ event: JSON) {
    let data = event["data"]
    switch event["topic"].text {
    case "transfer.progress": progress[data["transfer_id"].text] = data
    case "transfer.error": error = data["error"].text
    case "transfer.finished": progress.removeValue(forKey: data["transfer_id"].text)
    case "plan.awaiting_approval":
      approvals[data["plan_id"].text] = data
      onApproval?(data)
    case "drive.mounted":
      drives.removeAll { $0["mount_path"] == data["mount_path"] }
      drives.append(data)
    case "drive.unmounted": drives.removeAll { $0["mount_path"] == data["mount_path"] }
    default: break
    }
  }
  package func approve(_ plan: ReviewedPlan) async throws {
    guard plan.eligible else { throw RPCError(code: -32002, message: "Plan needs review") }
    var result: JSON = ["status": "approved"]
    if plan.status != "approved" { result = try await rpc("plan.approve", plan.reference) }
    if result["status"] == "approved" { result = try await rpc("transfer.start", plan.reference) }
    review = nil
    section = "Activity"
    if result["status"] == "needs_attention" { error = result["reason"].text }
    try await refresh()
  }
}
