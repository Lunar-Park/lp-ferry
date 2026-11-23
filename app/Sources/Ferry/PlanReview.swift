import FerryCore
import SwiftUI

struct PlanReview: View {
  let store: Store
  let plan: JSON
  @State private var selected = ""
  @State private var sourceLabel = ""
  @State private var role = ""
  @State private var busy = false
  var destination: String {
    selected.isEmpty ? (plan["destinations"].rows.first?["id"].text ?? "") : selected
  }
  var reviewed: ReviewedPlan? {
    try? JSONDecoder().decode(ReviewedPlan.self, from: JSONEncoder().encode(plan))
  }
  var body: some View {
    Panel(title: "Plan review") {
      Text("\(Format.bytes(plan["total_bytes"].count)) to copy").font(.inter(22, .semibold))
      Text(
        ["copy", "skip_identical", "dir", "exclude", "needs_review"].map {
          "\(plan["counts"][$0].count) \($0.replacingOccurrences(of: "_", with: " "))"
        }.joined(separator: "   ·   ")
      ).foregroundStyle(Palette.ink3)
      Detail(text: plan["fingerprint"].text)
      if plan["actor"].text.hasPrefix("agent:") {
        Text("Triggered by " + plan["actor"].text.dropFirst(6))
      }
      ForEach(plan["findings"].rows, id: \.self) { finding in
        Label(finding["message"].text, systemImage: "exclamationmark.triangle").foregroundStyle(
          Palette.warn)
      }
      ForEach(plan["destinations"].rows, id: \.self) { d in
        Detail(text: d["peer_id"].text + "  " + d["root_path"].text)
        Text(
          "\(d["label"].text) · \(Format.bytes(d["required_bytes"].count)) required · \(Format.bytes(d["free_bytes"].count)) free"
        )
      }
    }
    Panel(title: "Before → After") {
      Picker(
        "Destination",
        selection: Binding(
          get: { destination },
          set: {
            selected = $0
          })
      ) {
        ForEach(plan["destinations"].rows, id: \.self) { Text($0["label"].text).tag($0["id"].text) }
      }
      PlanBranch(store: store, planID: plan["plan_id"], destination: destination, exclude: exclude)
        .id(destination)
      DisclosureGroup("Revise plan") {
        VStack(alignment: .leading) {
          Menu("Change conflict policy") {
            ForEach(policies, id: \.self) { p in
              Button(p) { revise(["conflict_policy": .string(p)]) }
            }
          }
          TextField("Source label", text: $sourceLabel)
          TextField("Camera role", text: $role)
          Button("Assign role") { revise(["camera_roles": .object([sourceLabel: .string(role)])]) }
            .disabled(sourceLabel.isEmpty || role.isEmpty)
        }.padding(8)
      }
    }
    HStack {
      PillButton(title: "Close review") { store.review = nil }
      Spacer()
      PillButton(
        title: "Approve & Start", role: .primary,
        enabled: reviewed?.eligible == true && store.online
      ) {
        guard let reviewed else { return }
        run { try await store.approve(reviewed) }
      }
    }.disabled(busy)
  }
  func run(_ work: @escaping @MainActor () async throws -> Void) {
    guard !busy else { return }
    busy = true
    store.perform {
      defer { busy = false }
      try await work()
    }
  }
  func resolve(_ revision: JSON) async throws {
    store.review = try await store.rpc(
      "plan.resolve", ["plan_id": plan["plan_id"], "revision": revision])
  }
  func revise(_ revision: JSON) { run { try await resolve(revision) } }
  func exclude(_ node: JSON) {
    // Destination folders may combine several sources. Resolve against actual ledger entries.
    let dest = destination
    let path = node["path"].text
    run {
      var after = ""
      var exclusions = [JSON]()
      repeat {
        let result = try await store.rpc(
          "plan.entries",
          [
            "plan_id": plan["plan_id"], "destination_id": .string(dest), "cursor": .string(after),
            "limit": 1000,
          ])
        for e in result["entries"].rows
        where e["dest_rel_path"].text == path || e["dest_rel_path"].text.hasPrefix(path + "/") {
          exclusions.append([
            "source_label": e["source_label"], "rel": e["source_rel"],
            "reason": "Excluded during review",
          ])
        }
        after = result["next_cursor"].text
      } while !after.isEmpty
      if !exclusions.isEmpty { try await resolve(["exclude": .array(exclusions)]) }
    }
  }
}
