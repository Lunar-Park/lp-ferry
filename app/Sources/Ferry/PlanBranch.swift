import FerryCore
import SwiftUI

/// Each disclosed directory owns a cursor scoped to its immutable plan and destination.
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
    VStack(alignment: .leading, spacing: 9) {
      if parent.isEmpty && excluded > 0 {
        HStack {
          StateLabel(word: "excluded")
          Text("\(excluded) entries").font(.inter(12))
        }
      }
      ForEach(nodes, id: \.self) { node in
        let path = node["path"].text
        HStack(spacing: 10) {
          if node["kind"] == "dir" {
            Button {
              if !expanded.insert(path).inserted { expanded.remove(path) }
            } label: {
              HStack(spacing: 6) {
                Image(systemName: expanded.contains(path) ? "chevron.down" : "chevron.right").font(
                  .caption2)
                Image(systemName: "folder.fill").foregroundStyle(Palette.brand)
                Text(node["name"].text).font(.inter(14, .semibold))
              }
            }
            Text("\(node["file_count"].count) files · \(Format.bytes(node["total_bytes"].count))")
              .font(.inter(11)).foregroundStyle(Palette.ink3)
          } else {
            Image(systemName: "doc").foregroundStyle(Palette.brand)
            VStack(alignment: .leading, spacing: 3) {
              Text(node["name"].text).font(.inter(13, .semibold))
              Text(node["before"].text + "  →  " + path).font(
                .system(size: 10, design: .monospaced)
              ).foregroundStyle(Palette.ink3)
            }
          }
          Spacer()
          if node["action"].text != "" {
            StateLabel(word: node["action"] == "exclude" ? "excluded" : node["action"].text)
          }
          if node["renamed_count"].count > 0 { StateLabel(word: "renamed") }
          if !node["conflict_kind"].text.isEmpty && node["action"] != "needs_review" {
            Text(node["conflict_kind"].text.replacingOccurrences(of: "_", with: " ")).font(
              .inter(11)
            ).foregroundStyle(Palette.ink3)
          } else if node["kind"] == "dir" && node["conflict_count"].count > 0 {
            Text("\(node["conflict_count"].count) conflicts").font(.inter(11)).foregroundStyle(
              Palette.warn)
          }
          if node["kind"] == "file" {
            Button {
              editRole(node)
            } label: {
              Image(systemName: "camera")
            }.help("Assign camera role").accessibilityLabel("Assign camera role")
          }
          Button {
            exclude(node)
          } label: {
            Image(systemName: "minus.circle")
          }.help("Exclude " + path).accessibilityLabel("Exclude " + path)
        }
        if expanded.contains(path) {
          PlanBranch(
            store: store, planID: planID, destination: destination, parent: path, depth: depth + 1,
            editRole: editRole, exclude: exclude
          )
          .padding(.leading, depth < 8 ? 16 : 3).overlay(alignment: .leading) {
            Rectangle().fill(Palette.rim).frame(width: 1)
          }
        }
        Divider()
      }
      if !cursor.isEmpty || (!loaded && !loading) {
        Button(loaded ? "Load more" : "Load directory") { Task { await page() } }.disabled(loading)
      }
      if loaded && nodes.isEmpty { Text("Empty directory").foregroundStyle(Palette.ink4) }
    }.task { if !loaded { await page() } }
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
