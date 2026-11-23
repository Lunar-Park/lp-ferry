import FerryCore
import SwiftUI

/// The second stage: one card that says what will happen in a sentence, whether it fits, and
/// the one button that starts it; under it, the list of what lands where.
struct PlanReview: View {
  let store: Store
  let chrome: Chrome
  let plan: JSON
  @State private var selected = ""
  @State private var sourceLabel = ""
  @State private var role = ""
  @State private var editingRole = false
  @State private var busy = false

  var destination: String {
    selected.isEmpty ? (plan["destinations"].rows.first?["id"].text ?? "") : selected
  }
  var reviewed: ReviewedPlan? {
    try? JSONDecoder().decode(ReviewedPlan.self, from: JSONEncoder().encode(plan))
  }
  private var current: JSON {
    plan["destinations"].rows.first { $0["id"].text == destination } ?? .null
  }
  /// An agent's plan, held for a person to approve.
  private var request: Bool {
    plan["status"] == "awaiting_approval" || plan["actor"].text.hasPrefix("agent:")
  }

  var body: some View {
    PageFrame {
      PageHead(title: "Transfer") { StageMarker(reviewing: true) }
    } content: { _ in
      VStack(alignment: .leading, spacing: Space.s7) {
        card
        tree
      }
    }
    .task { await chrome.routes.load([plan["plan_id"].text], store: store) }
    .sheet(isPresented: $editingRole) {
      SheetPage(title: "Name the camera", height: 340) {
        FieldLabel("Card or folder")
        TextField("A004", text: $sourceLabel)
        FieldLabel("Camera name")
        TextField("A camera, B camera…", text: $role)
        Button("Update the plan") {
          editingRole = false
          revise(["camera_roles": .object([sourceLabel: .string(role)])])
        }
        .buttonStyle(.primary)
        .disabled(sourceLabel.isEmpty || role.isEmpty)
      }
    }
  }

  // MARK: The card

  private var title: String {
    if let route = chrome.routes.route(plan["plan_id"].text), !route.title.isEmpty, request {
      return route.title
    }
    let from = store.sources.map(\.label).joined(separator: ", ")
    let to = plan["destinations"].rows.map { $0["label"].text }.joined(separator: ", ")
    if !from.isEmpty && !request { return "\(from) → \(to)" }
    return chrome.routes.route(plan["plan_id"].text)?.title.nilIfEmpty ?? "To \(to)"
  }

  private var counts: String {
    let c = plan["counts"]
    var parts = [String]()
    let copy = c["copy"].count
    parts.append("\(copy) \(copy == 1 ? "file" : "files") · \(Format.bytes(plan["total_bytes"].count))")
    if c["skip_identical"].count > 0 { parts.append("\(c["skip_identical"].count) already there") }
    if c["exclude"].count > 0 { parts.append("\(c["exclude"].count) left out") }
    return parts.joined(separator: "  ·  ")
  }

  private var safe: Bool {
    let targets = plan["destinations"].rows
    return !targets.isEmpty
      && targets.allSatisfy {
        $0["free_bytes"] != .null
          && $0["free_bytes"].count - $0["required_bytes"].count >= $0["reserve_bytes"].count
      }
  }

  private var card: some View {
    VStack(alignment: .leading, spacing: Space.s5) {
      HStack(alignment: .top, spacing: Space.s5) {
        VStack(alignment: .leading, spacing: Space.s2) {
          if request {
            HStack(spacing: 6) {
              Image(systemName: "hand.raised").font(.system(size: 11, weight: .semibold))
              Text("Requested by \(Routes.actor(plan))").font(.inter(13, .semibold))
            }
            .foregroundStyle(Palette.warn)
          }
          Text(title).font(.inter(26, .semibold)).tracking(-0.5).foregroundStyle(Palette.bone)
            .lineLimit(2)
          Text(counts).font(.inter(TypeScale.base)).foregroundStyle(Palette.ink2)
          if plan["counts"]["needs_review"].count > 0 {
            Text("\(plan["counts"]["needs_review"].count) need a decision before copying")
              .font(.inter(TypeScale.sm, .medium)).foregroundStyle(Palette.warn)
          }
        }
        Spacer(minLength: Space.s4)
        HStack(spacing: Space.s2) {
          Button("Back") { store.review = nil }
            .buttonStyle(.quiet)
            .keyboardShortcut(.cancelAction)
          Button {
            guard let reviewed else { return }
            run { try await store.approve(reviewed) }
          } label: {
            Label(request ? "Approve & start" : "Start verified copy", systemImage: "checkmark")
          }
          .buttonStyle(.primary)
          .keyboardShortcut(.defaultAction)
          .disabled(reviewed?.eligible != true || !store.online || busy)
        }
      }
      ForEach(plan["findings"].rows, id: \.self) { finding in
        HStack(alignment: .firstTextBaseline, spacing: Space.s2) {
          Image(systemName: "exclamationmark.triangle").font(.system(size: 11, weight: .semibold))
          Text(finding["message"].text).font(.inter(TypeScale.sm))
        }
        .foregroundStyle(Palette.warn)
      }
      Hairline()
      HStack(spacing: Space.s4) {
        Image(systemName: safe ? "checkmark.shield" : "exclamationmark.triangle")
          .font(.system(size: 15, weight: .medium))
          .foregroundStyle(safe ? Palette.ok : Palette.warn)
        VStack(alignment: .leading, spacing: 3) {
          Text(safe ? "Fits on \(current["label"].text)" : "Not enough room on \(current["label"].text)")
            .font(.inter(TypeScale.sm, .semibold)).foregroundStyle(Palette.ink1)
          Text(
            "Needs \(Format.bytes(current["required_bytes"].count)) of \(Format.bytes(current["free_bytes"].count)) free. Every file is read back and checked after it lands."
          )
          .font(.inter(13)).foregroundStyle(Palette.ink3).fixedSize(horizontal: false, vertical: true)
        }
        Spacer(minLength: Space.s4)
        let share = current["required_bytes"].double / max(1, current["free_bytes"].double)
        if share >= 0.1 || !safe {
          Meter(fraction: share, color: safe ? Palette.accent : Palette.warn).frame(width: 140)
        }
      }
      Fold(title: "Details") {
        VStack(alignment: .leading, spacing: Space.s3) {
          fact("Checked with", plan["checksum_algo"].text.uppercased())
          fact("Landing in", [current["peer_id"].text == "local" ? "" : current["peer_id"].text, current["root_path"].text].filter { !$0.isEmpty }.joined(separator: " · "), mono: true)
          fact("Plan", plan["plan_id"].text + " · " + plan["fingerprint"].text, mono: true)
          HStack(spacing: Space.s3) {
            GlassMenu(title: "If a file is already there", size: .small) {
              ForEach(policies, id: \.self) { p in
                Button(policyName(p)) { revise(["conflict_policy": .string(p)]) }
              }
            }
            Button("Name a camera…") {
              sourceLabel = store.sources.first?.label ?? ""
              role = ""
              editingRole = true
            }
            .buttonStyle(.park(.quiet, .small))
          }
        }
        .frame(maxWidth: 560, alignment: .leading)
      }
    }
    .padding(Space.s6)
    .background(GlassSurface(radius: 22))
    .disabled(busy)
  }

  private func fact(_ name: String, _ value: String, mono: Bool = false) -> some View {
    HStack(alignment: .firstTextBaseline, spacing: Space.s4) {
      Text(name).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3).frame(width: 96, alignment: .leading)
      Text(value).font(mono ? .mono(11.5) : .inter(TypeScale.xs, .medium)).foregroundStyle(Palette.ink1)
        .lineLimit(1).truncationMode(.middle).textSelection(.enabled)
    }
  }

  // MARK: The tree

  private var tree: some View {
    VStack(alignment: .leading, spacing: 0) {
      HStack(alignment: .center, spacing: Space.s3) {
        SectionTitle("What lands where")
        if plan["destinations"].rows.count > 1 {
          GlassMenu(title: current["label"].text, size: .small) {
            ForEach(plan["destinations"].rows, id: \.self) { d in
              Button(d["label"].text) { selected = d["id"].text }
            }
          }
        }
      }
      .padding(.bottom, Space.s3)
      Hairline(color: Palette.rule2)
      PlanBranch(
        store: store, planID: plan["plan_id"], destination: destination,
        editRole: { node in
          sourceLabel = String(node["before"].text.split(separator: ":", maxSplits: 1).first ?? "")
          role = ""
          editingRole = true
        }, exclude: exclude
      )
      .id(destination)
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
