import FerryCore
import SwiftUI

/// The plan under review: the before → after tree as an open list on the room, and beside it
/// one glass inspector with the destination, the space check and the decision.
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
  private var current: JSON {
    plan["destinations"].rows.first { $0["id"].text == destination } ?? .null
  }

  var body: some View {
    PageFrame {
      PageHead(title: "Transfer") { Steps(active: stage) }
    } content: { width in
      Columns(width: width, side: 320) {
        tree
      } side: {
        inspector
      }
    }
    .sheet(isPresented: $editingRole) {
      SheetPage(title: "Assign camera role", height: 340) {
        FieldLabel("Source label")
        TextField("Source label", text: $sourceLabel)
        FieldLabel("Camera role")
        TextField("A camera, B camera…", text: $role)
        Button("Revise plan") {
          editingRole = false
          revise(["camera_roles": .object([sourceLabel: .string(role)])])
        }
        .buttonStyle(.primary)
        .disabled(sourceLabel.isEmpty || role.isEmpty)
      }
    }
  }

  // MARK: The tree

  private var tree: some View {
    VStack(alignment: .leading, spacing: Space.s5) {
      VStack(alignment: .leading, spacing: Space.s2) {
        HStack(alignment: .firstTextBaseline, spacing: Space.s3) {
          Text(Format.bytes(plan["total_bytes"].count)).font(.inter(TypeScale.xl, .semibold))
            .tracking(-0.6).foregroundStyle(Palette.bone)
          Text("to copy").font(.inter(TypeScale.lg)).foregroundStyle(Palette.ink3)
        }
        HStack(spacing: Space.s2) {
          ForEach(["copy", "skip_identical", "dir", "exclude", "needs_review"], id: \.self) { key in
            if plan["counts"][key].count > 0 {
              HStack(spacing: 5) {
                Text("\(plan["counts"][key].count)").font(.inter(13, .semibold)).foregroundStyle(Palette.ink1)
                Text(key == "dir" ? "folders" : display(key).lowercased()).font(.inter(13))
                  .foregroundStyle(Palette.ink3)
              }
              .padding(.horizontal, 10).frame(height: 24)
              .background(Capsule().fill(Color.white.opacity(0.05)))
            }
          }
          if plan["actor"].text.hasPrefix("agent:") {
            Text("Requested by " + plan["actor"].text.dropFirst(6)).font(.inter(13, .medium))
              .foregroundStyle(Palette.warn)
              .padding(.horizontal, 10).frame(height: 24)
              .background(Capsule().fill(Palette.warn.opacity(0.12)))
          }
        }
      }
      ForEach(plan["findings"].rows, id: \.self) { finding in
        HStack(alignment: .firstTextBaseline, spacing: Space.s2) {
          Image(systemName: "exclamationmark.triangle").font(.system(size: 11, weight: .semibold))
          Text(finding["message"].text).font(.inter(TypeScale.sm))
        }
        .foregroundStyle(Palette.warn)
      }
      VStack(alignment: .leading, spacing: 0) {
        HStack(alignment: .center) {
          SectionTitle("Before → After")
          if plan["destinations"].rows.count > 1 {
            GlassMenu(title: current["label"].text, icon: "externaldrive", size: .small) {
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
  }

  // MARK: The inspector

  private var inspector: some View {
    let targets = plan["destinations"].rows
    let safe =
      !targets.isEmpty
      && targets.allSatisfy {
        $0["free_bytes"] != .null
          && $0["free_bytes"].count - $0["required_bytes"].count >= $0["reserve_bytes"].count
      }
    return VStack(alignment: .leading, spacing: Space.s5) {
      VStack(alignment: .leading, spacing: Space.s4) {
        FieldLabel("Landing on")
        HStack(spacing: Space.s3) {
          IconDisc(symbol: current["peer_id"].text.isEmpty ? "externaldrive" : "desktopcomputer", size: 38)
          VStack(alignment: .leading, spacing: 4) {
            Text(current["label"].text).font(.inter(TypeScale.md, .semibold)).foregroundStyle(Palette.bone)
            Detail(
              text: [current["peer_id"].text, current["root_path"].text].filter { !$0.isEmpty }
                .joined(separator: "  "))
          }
        }
        VStack(alignment: .leading, spacing: 6) {
          Meter(
            fraction: current["required_bytes"].double / max(1, current["free_bytes"].double),
            color: safe ? Palette.accent : Palette.warn)
          HStack {
            Text(Format.bytes(current["required_bytes"].count) + " needed")
            Spacer()
            Text(Format.bytes(current["free_bytes"].count) + " free")
          }
          .font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
        }
        HStack(spacing: Space.s2) {
          Image(systemName: safe ? "checkmark.shield" : "exclamationmark.triangle")
            .font(.system(size: 13, weight: .medium))
          Text(safe ? "Space check passed" : "Check destination space").font(.inter(TypeScale.sm, .medium))
        }
        .foregroundStyle(safe ? Palette.ok : Palette.warn)
        Hairline()
        fact("Fingerprint", plan["fingerprint"].text, mono: true)
        fact("Checksum", plan["checksum_algo"].text.uppercased(), mono: false)
        Fold(title: "Camera roles & conflict policy") {
          VStack(alignment: .leading, spacing: Space.s3) {
            GlassMenu(title: "Conflict policy", icon: "arrow.triangle.branch", size: .small) {
              ForEach(policies, id: \.self) { p in
                Button(display(p)) { revise(["conflict_policy": .string(p)]) }
              }
            }
            TextField("Source label", text: $sourceLabel)
            TextField("Camera role", text: $role)
            Button("Assign role") {
              revise(["camera_roles": .object([sourceLabel: .string(role)])])
            }
            .buttonStyle(.park(.standard, .small))
            .disabled(sourceLabel.isEmpty || role.isEmpty)
          }
        }
        Hairline()
        HStack(spacing: Space.s3) {
          Button {
            guard let reviewed else { return }
            run {
              stage = 3
              defer { stage = 2 }
              try await store.approve(reviewed) { stage = 4 }
            }
          } label: {
            Label("Approve & start", systemImage: "checkmark")
          }
          .buttonStyle(.primary)
          .disabled(reviewed?.eligible != true || !store.online)
          Button("Close") { store.review = nil }.buttonStyle(.quiet)
        }
      }
      .padding(Space.s6)
      .background(GlassSurface(radius: 20))
      .disabled(busy)
      Text("Nothing is copied until you approve. Every file is read back and checked on landing.")
        .font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink4)
        .fixedSize(horizontal: false, vertical: true)
        .padding(.horizontal, Space.s2)
    }
  }

  private func fact(_ name: String, _ value: String, mono: Bool) -> some View {
    HStack(alignment: .firstTextBaseline) {
      Text(name).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
      Spacer(minLength: Space.s3)
      Text(value).font(mono ? .mono(11.5) : .inter(TypeScale.xs, .medium)).foregroundStyle(Palette.ink1)
        .lineLimit(1).truncationMode(.middle).textSelection(.enabled).help(value)
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
