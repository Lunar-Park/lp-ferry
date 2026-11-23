import FerryCore
import SwiftUI

/// One level of the before → after tree, as open rows split by hairlines. Each disclosed
/// directory owns a cursor scoped to its immutable plan and destination.
struct PlanBranch: View {
  let store: Store
  let planID: JSON
  let destination: String
  var parent = ""
  var depth = 0
  let editRole: (JSON) -> Void
  let exclude: (JSON) -> Void
  @State private var nodes = [JSON]()
  @State private var expanded = Set<String>()
  @State private var cursor = ""
  @State private var loading = false
  @State private var loaded = false
  @State private var excluded: Int64 = 0

  var body: some View {
    VStack(alignment: .leading, spacing: 0) {
      if parent.isEmpty && excluded > 0 {
        HStack(spacing: Space.s2) {
          StatusPill(word: "excluded")
          Text("\(excluded) entries left out of this plan").font(.inter(TypeScale.xs))
            .foregroundStyle(Palette.ink3)
        }
        .padding(.vertical, Space.s3)
        Hairline()
      }
      ForEach(Array(nodes.enumerated()), id: \.offset) { index, node in
        let path = node["path"].text
        if index > 0 { Hairline() }
        row(node, path: path)
        if expanded.contains(path) {
          PlanBranch(
            store: store, planID: planID, destination: destination, parent: path, depth: depth + 1,
            editRole: editRole, exclude: exclude
          )
          .padding(.leading, depth < 8 ? 28 : 4)
          .overlay(alignment: .leading) {
            Rectangle().fill(Palette.rule2).frame(width: 1).padding(.leading, 11).padding(.vertical, 4)
          }
        }
      }
      if !cursor.isEmpty || (!loaded && !loading) {
        Hairline()
        Button(loaded ? "Load more" : "Load directory") { Task { await page() } }
          .buttonStyle(.park(.quiet, .small))
          .disabled(loading)
          .padding(.vertical, Space.s2)
      }
      if loaded && nodes.isEmpty {
        Text("Empty directory").font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink4)
          .padding(.vertical, Space.s3)
      }
    }
    .task { if !loaded { await page() } }
  }

  @ViewBuilder private func row(_ node: JSON, path: String) -> some View {
    let dir = node["kind"] == "dir"
    HStack(spacing: Space.s3) {
      if dir {
        Button {
          withAnimation(.spring(response: 0.3, dampingFraction: 0.85)) {
            if !expanded.insert(path).inserted { expanded.remove(path) }
          }
        } label: {
          HStack(spacing: Space.s2) {
            Image(systemName: "chevron.right").font(.system(size: 9, weight: .bold))
              .foregroundStyle(Palette.ink3)
              .rotationEffect(.degrees(expanded.contains(path) ? 90 : 0))
              .frame(width: 12)
            Image(systemName: "folder").font(.system(size: 15)).foregroundStyle(Palette.accent)
            Text(node["name"].text).font(.inter(TypeScale.base, .semibold)).foregroundStyle(Palette.bone)
            Text("\(node["file_count"].count) files · \(Format.bytes(node["total_bytes"].count))")
              .font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
          }
          .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
      } else {
        Image(systemName: "doc").font(.system(size: 13)).foregroundStyle(Palette.ink3).frame(width: 12)
        VStack(alignment: .leading, spacing: 4) {
          HStack(spacing: Space.s2) {
            Text(node["name"].text).font(.inter(TypeScale.sm, .semibold)).foregroundStyle(Palette.ink1)
            if node["total_bytes"] != .null {
              Text(Format.bytes(node["total_bytes"].count)).font(.inter(TypeScale.xs))
                .foregroundStyle(Palette.ink4)
            }
          }
          HStack(spacing: 6) {
            Text(node["before"].text)
            Image(systemName: "arrow.right").font(.system(size: 8, weight: .bold)).foregroundStyle(Palette.accent)
            Text(path).foregroundStyle(Palette.ink2)
          }
          .font(.mono(11)).foregroundStyle(Palette.ink4).lineLimit(1).truncationMode(.middle)
        }
      }
      Spacer(minLength: Space.s3)
      if !node["conflict_kind"].text.isEmpty && node["action"] != "needs_review" {
        Text(display(node["conflict_kind"].text)).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
      } else if dir && node["conflict_count"].count > 0 {
        Text("\(node["conflict_count"].count) conflicts").font(.inter(TypeScale.xs, .medium))
          .foregroundStyle(Palette.warn)
      }
      if node["renamed_count"].count > 0 { StatusPill(word: "renamed") }
      if node["action"].text != "" {
        StatusPill(word: node["action"] == "exclude" ? "excluded" : node["action"].text)
      }
      HStack(spacing: 0) {
        if !dir {
          Button {
            editRole(node)
          } label: {
            Image(systemName: "camera").font(.system(size: 11, weight: .medium))
          }
          .buttonStyle(.park(.quiet, .small, disc: true))
          .help("Assign camera role").accessibilityLabel("Assign camera role")
        }
        Button {
          exclude(node)
        } label: {
          Image(systemName: "minus.circle").font(.system(size: 12, weight: .medium))
        }
        .buttonStyle(.park(.quiet, .small, disc: true))
        .help("Exclude " + path).accessibilityLabel("Exclude " + path)
      }
      .frame(width: 52, alignment: .trailing)
    }
    .padding(.vertical, dir ? Space.s3 : 10)
  }

  func page() async {
    guard !loading else { return }
    loading = true
    defer { loading = false }
    do {
      let result = try await store.rpc(
        "plan.tree",
        [
          "plan_id": planID, "destination_id": .string(destination), "parent_path": .string(parent),
          "cursor": .string(cursor), "limit": 50,
        ])
      try Task.checkCancellation()
      if !loaded && depth == 0,
        let first = result["children"].rows.first(where: { $0["kind"] == "dir" })
      {
        expanded.insert(first["path"].text)
      }
      nodes += result["children"].rows
      excluded = result["excluded_count"].count
      cursor = result["next_cursor"].text
      loaded = true
    } catch { if !Task.isCancelled { store.error = error.localizedDescription } }
  }
}
