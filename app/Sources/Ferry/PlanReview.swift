import FerryCore
import SwiftUI

struct PlanReview: View {
  let store: Store
  let plan: JSON
  @State private var selected = ""
  @State private var sourceLabel = ""
  @State private var role = ""
  @State private var editingRole = false
  @State private var stage = 2
  @State private var busy = false
  var destination: String {
    selected.isEmpty ? (plan["destinations"].rows.first?["id"].text ?? "") : selected
  }
  var reviewed: ReviewedPlan? {
    try? JSONDecoder().decode(ReviewedPlan.self, from: JSONEncoder().encode(plan))
  }
  var body: some View {
    VStack(spacing: 0) {
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          Text("Transfer").font(.inter(31, .semibold))
          Pipeline(active: stage)
          Panel(title: "Plan review") {
            HStack(alignment: .firstTextBaseline, spacing: 16) {
              Text("\(Format.bytes(plan["total_bytes"].count)) to copy").font(.inter(22, .semibold))
              Text(
                ["copy", "skip_identical", "dir", "exclude", "needs_review"].filter {
                  plan["counts"][$0].count > 0
                }.map {
                  "\(plan["counts"][$0].count) \($0.replacingOccurrences(of: "_", with: " "))"
                }.joined(separator: "   ·   ")
              ).font(.inter(12)).foregroundStyle(Palette.ink3)
            }

            if plan["actor"].text.hasPrefix("agent:") {
              Text("Triggered by " + plan["actor"].text.dropFirst(6))
            }
            ForEach(plan["findings"].rows, id: \.self) { finding in
              Label(finding["message"].text, systemImage: "exclamationmark.triangle")
                .foregroundStyle(
                  Palette.warn)
            }
            ForEach(plan["destinations"].rows, id: \.self) { d in
              VStack(alignment: .leading, spacing: 4) {
                Detail(
                  text: [d["peer_id"].text, d["root_path"].text].filter { !$0.isEmpty }.joined(
                    separator: "  "))
                Text(
                  "\(d["label"].text) · \(Format.bytes(d["required_bytes"].count)) required · \(Format.bytes(d["free_bytes"].count)) free"
                ).font(.inter(13))
              }
            }
          }
          Panel(title: "Before → After") {
            HStack {
              ColumnLabel("Destination")
              FerryMenu(
                title: plan["destinations"].rows.first(where: { $0["id"].text == destination })?[
                  "label"
                ].text ?? "Choose target"
              ) {
                ForEach(plan["destinations"].rows, id: \.self) { d in
                  Button(d["label"].text) { selected = d["id"].text }
                }
              }
            }
            Divider()
            PlanBranch(
              store: store, planID: plan["plan_id"], destination: destination,
              editRole: { node in
                sourceLabel = String(
                  node["before"].text.split(separator: ":", maxSplits: 1).first ?? "")
                role = ""
                editingRole = true
              }, exclude: exclude
            )
            .id(destination)
            DisclosureGroup("Camera roles & conflict policy") {
              VStack(alignment: .leading) {
                FerryMenu(title: "Change conflict policy") {
                  ForEach(policies, id: \.self) { p in
                    Button(p) { revise(["conflict_policy": .string(p)]) }
                  }
                }
                TextField("Source label", text: $sourceLabel)
                TextField("Camera role", text: $role)
                Button("Assign role") {
                  revise(["camera_roles": .object([sourceLabel: .string(role)])])
                }
                .disabled(sourceLabel.isEmpty || role.isEmpty)
              }.padding(8)
            }
          }
        }.padding(24).frame(maxWidth: .infinity, alignment: .leading).overlayScrollers()
      }
      HStack {
        VStack(alignment: .leading, spacing: 4) {
          let targets = plan["destinations"].rows
          let safe =
            !targets.isEmpty
            && targets.allSatisfy {
              $0["free_bytes"] != .null
                && $0["free_bytes"].count - $0["required_bytes"].count >= $0["reserve_bytes"].count
            }
          Label(
            safe ? "Space check passed" : "Check destination space",
            systemImage: safe ? "checkmark.shield" : "exclamationmark.triangle"
          ).foregroundStyle(safe ? Palette.ok : Palette.warn)
          Text("Fingerprint  " + plan["fingerprint"].text).font(
            .system(size: 10, design: .monospaced)
          ).lineLimit(1).truncationMode(.middle).help(plan["fingerprint"].text)
        }
        Spacer()
        PillButton(title: "Close review") { store.review = nil }
        PillButton(
          title: "Approve & Start", role: .primary,
          enabled: reviewed?.eligible == true && store.online
        ) {
          guard let reviewed else { return }
          run {
            stage = 3
            defer { stage = 2 }
            try await store.approve(reviewed) { stage = 4 }
          }
        }
      }.padding(16).background(Palette.rail).overlay(alignment: .top) { Divider() }.disabled(busy)
    }.sheet(isPresented: $editingRole) {
      SheetPage(title: "Assign camera role") {
        TextField("Source label", text: $sourceLabel)
        TextField("Camera role", text: $role)
        PillButton(
          title: "Revise plan", role: .primary, enabled: !sourceLabel.isEmpty && !role.isEmpty
        ) {
          editingRole = false
          revise(["camera_roles": .object([sourceLabel: .string(role)])])
        }
      }
    }
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
