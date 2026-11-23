import FerryCore
import SwiftUI

/// Each disclosed directory owns a cursor scoped to its immutable plan and destination.
struct PlanBranch: View {
  let store: Store
  let planID: JSON
  let destination: String
  var parent = ""
  var depth = 0
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
        Text("\(excluded) excluded from this destination").font(.inter(12))
      }
      ForEach(nodes, id: \.self) { node in
        let path = node["path"].text
        HStack {
          if node["kind"] == "dir" {
            Button {
              if !expanded.insert(path).inserted { expanded.remove(path) }
            } label: {
              Label(
                node["name"].text,
                systemImage: expanded.contains(path) ? "chevron.down" : "chevron.right")
            }.buttonStyle(.plain)
            Text("\(node["file_count"].count) files · \(Format.bytes(node["total_bytes"].count))")
              .font(.inter(12)).foregroundStyle(Palette.ink3)
          } else {
            Detail(text: node["before"].text + "  →  " + path)
          }
          Spacer()
          Button("Exclude") { exclude(node) }
        }
        let badges = [
          node["action"].text, node["conflict_kind"].text,
          node["conflict_count"].count > 0 ? "\(node["conflict_count"].count) conflicts" : "",
          node["renamed_count"].count > 0 ? "renamed" : "",
        ].filter { !$0.isEmpty }
        if !badges.isEmpty {
          Text(badges.joined(separator: " · ")).font(.inter(11)).foregroundStyle(Palette.ink4)
        }
        if expanded.contains(path) {
          PlanBranch(
            store: store, planID: planID, destination: destination, parent: path, depth: depth + 1,
            exclude: exclude
          )
          .padding(.leading, depth < 4 ? 12 : 0)
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
      nodes += result["children"].rows
      excluded = result["excluded_count"].count
      cursor = result["next_cursor"].text
      loaded = true
    } catch { if !Task.isCancelled { store.error = error.localizedDescription } }
  }
}
