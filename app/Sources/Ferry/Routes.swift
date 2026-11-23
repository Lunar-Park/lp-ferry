import FerryCore
import SwiftUI

/// Human titles for transfers and requests: "A004 → Studio archive", never a bare id.
///
/// `transfer.list` carries only ids, so a route is read once per plan from `plan.get` (its
/// destinations) and the first `plan.entries` row (its source label), then kept. Until it
/// arrives, a title falls back to what the live progress knows, then to the date.
@MainActor @Observable final class Routes {
  struct Route: Equatable {
    var from: String
    var to: [String]
    var files: Int64
    var bytes: Int64

    var title: String {
      let target =
        to.isEmpty ? "" : to.count == 1 ? to[0] : "\(to[0]) + \(to.count - 1) more"
      switch (from.isEmpty, target.isEmpty) {
      case (false, false): return "\(from) → \(target)"
      case (false, true): return from
      case (true, false): return "To \(target)"
      default: return ""
      }
    }
  }

  private(set) var known: [String: Route] = [:]
  private var pending = Set<String>()

  func route(_ planID: String) -> Route? { known[planID] }

  /// Read the routes of these plans that are not known yet.
  func load(_ planIDs: [String], store: Store) async {
    for id in planIDs where !id.isEmpty && known[id] == nil && !pending.contains(id) {
      pending.insert(id)
      defer { pending.remove(id) }
      do {
        let plan = try await store.rpc("plan.get", ["plan_id": .string(id)])
        let first = try await store.rpc("plan.entries", ["plan_id": .string(id), "limit": 1])
        known[id] = Route(
          from: first["entries"].rows.first?["source_label"].text ?? "",
          to: plan["destinations"].rows.map { $0["label"].text }.filter { !$0.isEmpty },
          files: plan["counts"]["copy"].count, bytes: plan["total_bytes"].count)
      } catch {
        // A plan the daemon no longer has keeps its fallback title.
      }
    }
  }

  /// A transfer's title.
  func title(transfer row: JSON, live: JSON?) -> String {
    if let route = known[row["plan_id"].text], !route.title.isEmpty { return route.title }
    let labels = (live?["targets"].rows ?? []).map { $0["label"].text }.filter { !$0.isEmpty }
    if let first = labels.first { return "To \(first)" }
    let day = Stamp.day(row["started_at"].text.isEmpty ? row["created_at"].text : row["started_at"].text)
    return day.isEmpty ? "Transfer" : "Transfer · \(day)"
  }

  /// A request's title: who asked, and where it would land.
  func title(request row: JSON) -> String {
    let who = Self.actor(row)
    let to =
      known[row["plan_id"].text]?.to.first
      ?? row["destinations"].rows.first?["label"].text ?? ""
    return to.isEmpty ? "\(who)’s request" : "\(who) · \(to)"
  }

  /// "Claude wants to copy 42 files → Studio archive".
  func sentence(request row: JSON) -> String {
    let who = Self.actor(row)
    let route = known[row["plan_id"].text]
    let files = route?.files ?? row["counts"]["copy"].count
    let to =
      route?.to.first ?? row["destinations"].rows.first?["label"].text ?? "a destination"
    let what = files > 0 ? "\(files) \(files == 1 ? "file" : "files")" : "files"
    return "\(who) wants to copy \(what) → \(to)"
  }

  static func actor(_ row: JSON) -> String {
    let who = row["actor"].text.replacingOccurrences(of: "agent:", with: "")
    if who.isEmpty { return "An agent" }
    return who == "human" ? "You" : who
  }
}

/// Dates as rows show them: "Today 11:15", "Nov 22, 11:23".
enum Stamp {
  static func day(_ iso: String) -> String {
    guard !iso.isEmpty else { return "" }
    let parser = ISO8601DateFormatter()
    guard let date = parser.date(from: iso) else { return iso }
    if Calendar.current.isDateInToday(date) {
      return "Today " + date.formatted(.dateTime.hour().minute())
    }
    return date.formatted(.dateTime.month(.abbreviated).day().hour().minute())
  }
}
