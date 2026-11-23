import FerryCore
import SwiftUI
import UniformTypeIdentifiers

private struct Editor: Identifiable {
  enum Kind { case preset, destination, camera }
  let kind: Kind
  var row: JSON = .null
  let id = UUID()
}
struct Library: View {
  let store: Store
  @State private var editor: Editor?
  @State private var previewID = ""
  @State private var scope = "All"
  var body: some View {
    Segments(choices: ["All", "Destinations", "Presets"], selection: $scope)
    VStack(spacing: 16) {
      if scope != "Presets" {
        Panel(
          title: "Saved destinations", actionTitle: "New destination",
          action: { editor = Editor(kind: .destination) }
        ) {
          ForEach(store.destinations, id: \.self) { row in
            Divider()
            let resolution = row["resolution"]
            HStack {
              Image(systemName: "externaldrive").foregroundStyle(Palette.brand)
              VStack(alignment: .leading, spacing: 3) {
                Text(row["name"].text).font(.inter(15, .semibold))
                Detail(text: row["root_path"].text)
              }
              Spacer()
              StateLabel(word: resolution["state"].text)
              FerryMenu(title: "Manage") {
                Button("Check binding") {
                  store.perform {
                    let updated = try await store.rpc("dest.resolve", ["id": row["id"]])
                    if let index = store.destinations.firstIndex(where: { $0["id"] == row["id"] }) {
                      store.destinations[index] = updated
                    }
                  }
                }
                Button("Edit…") { editor = Editor(kind: .destination, row: row) }
                Button("Rebind folder…") {
                  if let path = pickFolders(multiple: false).first?.path {
                    mutate("dest.rebind", ["id": row["id"], "path": .string(path)])
                  }
                }
                Button("Archive") { mutate("dest.archive", ["id": row["id"]]) }
              }
            }
            if !resolution["reason"].text.isEmpty {
              Text(resolution["reason"].text).font(.inter(12))
            }
          }
        }
      }
      if scope != "Destinations" {
        Panel(title: "Organization presets") {
          ForEach(store.presets, id: \.self) { row in
            HStack {
              VStack(alignment: .leading) {
                Text(row["name"].text).font(.inter(16, .semibold))
                Detail(text: "Revision \(row["latest_revision"].count)")
              }
              Spacer()
              PillButton(title: "Open") { editor = Editor(kind: .preset, row: row) }
              Button("Export…") {
                let panel = NSSavePanel()
                panel.allowedContentTypes = [.json]
                panel.nameFieldStringValue = row["name"].text + ".json"
                guard panel.runModal() == .OK, let url = panel.url else { return }
                store.perform {
                  let data = try await store.rpc("preset.export", ["id": row["id"]])
                  try data.pretty.write(to: url, atomically: true, encoding: .utf8)
                }
              }
            }
            Divider()
          }
          HStack {
            PillButton(title: "New preset", role: .primary) { editor = Editor(kind: .preset) }
            PillButton(title: "Import JSON…") {
              let panel = NSOpenPanel()
              panel.allowedContentTypes = [.json]
              guard panel.runModal() == .OK, let url = panel.url else { return }
              store.perform {
                let doc = try JSONDecoder().decode(JSON.self, from: Data(contentsOf: url))
                _ = try await store.rpc("preset.import", ["json": doc])
                try await store.refresh()
              }
            }
            PillButton(title: "Bind camera…") { editor = Editor(kind: .camera) }
          }
        }
        if let row = store.presets.first(where: { $0["id"].text == previewID })
          ?? store.presets.first
        {
          Panel(title: "Preview saved revision") {
            FerryMenu(title: row["name"].text) {
              ForEach(store.presets, id: \.self) { p in
                Button(p["name"].text) { previewID = p["id"].text }
              }
            }
            PresetPreview(store: store, row: row)
          }
        }
      }
    }.sheet(item: $editor) { editor in
      switch editor.kind {
      case .destination: DestinationEditor(store: store, row: editor.row)
      case .preset: PresetEditor(store: store, row: editor.row)
      case .camera: CameraEditor(store: store)
      }
    }
  }
  func mutate(_ method: String, _ params: JSON) {
    store.perform {
      _ = try await store.rpc(method, params)
      try await store.refresh()
    }
  }
}
private struct DestinationEditor: View {
  let store: Store
  let row: JSON
  @Environment(\.dismiss) var dismiss
  @State private var name = ""
  @State private var path = ""
  @State private var message = ""
  var body: some View {
    SheetPage(title: "Saved destination") {
      TextField("Name", text: $name)
      HStack {
        TextField("Folder", text: $path)
        Button("Choose…") { if let url = pickFolders(multiple: false).first { path = url.path } }
      }
      PillButton(
        title: "Save destination", role: .primary, enabled: !name.isEmpty && path.hasPrefix("/")
      ) {
        var fields: [String: JSON] = ["name": .string(name), "path": .string(path)]
        for (k, v) in [
          ("id", "id"), ("preset_id", "pinned_preset_id"), ("preset_rev", "pinned_preset_rev"),
          ("default_conflict", "default_conflict"), ("checksum_algo", "checksum_algo"),
          ("reserve_bytes", "reserve_bytes"),
        ] where row[v] != .null { fields[k] = row[v] }
        Task {
          do {
            _ = try await store.rpc("dest.save", .object(fields))
            try await store.refresh()
            dismiss()
          } catch { message = error.localizedDescription }
        }
      }
      Text(message)
    }.onAppear {
      name = row["name"].text
      path = row["root_path"].text
    }
  }
}
private struct CameraEditor: View {
  let store: Store
  @State private var fields = [String: String]()
  @State private var message = ""
  @Environment(\.dismiss) var dismiss
  let keys = ["make", "model", "serial", "role"]
  var body: some View {
    SheetPage(title: "Bind camera role") {
      ForEach(keys, id: \.self) { key in
        TextField(
          key.capitalized, text: Binding(get: { fields[key] ?? "" }, set: { fields[key] = $0 }))
      }
      PillButton(
        title: "Save binding", role: .primary,
        enabled: keys.allSatisfy { !(fields[$0] ?? "").isEmpty }
      ) {
        Task {
          do {
            _ = try await store.rpc("camera.bind", .object(fields.mapValues(JSON.string)))
            dismiss()
          } catch { message = error.localizedDescription }
        }
      }
      Text(message)
    }
  }
}
private struct PresetEditor: View {
  let store: Store
  let row: JSON
  @State private var json = ""
  @State private var message = ""
  @Environment(\.dismiss) var dismiss
  var body: some View {
    SheetPage(title: row["name"].text.isEmpty ? "New preset" : row["name"].text) {
      Text("Rules, groups & exclusions").font(.inter(15, .semibold))
      TextEditor(text: $json).font(.system(size: 12, design: .monospaced)).frame(height: 220)
      PillButton(
        title: row["builtin"] == .bool(true) ? "Save as new preset" : "Save revision",
        role: .primary, enabled: !json.isEmpty
      ) {
        Task {
          do {
            let doc = try JSONDecoder().decode(JSON.self, from: Data(json.utf8))
            var p: [String: JSON] = ["preset": doc]
            if row["id"] != .null && row["builtin"] != .bool(true) { p["id"] = row["id"] }
            _ = try await store.rpc("preset.save", .object(p))
            try await store.refresh()
            dismiss()
          } catch { message = error.localizedDescription }
        }
      }
      if row["id"] != .null {
        PresetPreview(store: store, row: row)
      }
      Text(message)
    }.task {
      if row["id"] != .null {
        do { json = try await store.rpc("preset.get", ["id": row["id"]])["preset"].pretty } catch {
          message = error.localizedDescription
        }
      } else {
        let doc: JSON = [
          "schema": "ferry-preset-v1", "name": "Untitled", "rules": [],
          "fallback": "{relative_dir}/{filename}", "conflict_policy": "keep_both",
        ]
        json = doc.pretty
      }
    }
  }
}

private struct PresetPreview: View {
  let store: Store
  let row: JSON
  @State private var paths = "DCIM/Clip001.mov"
  @State private var label = "Camera A"
  @State private var message = ""
  @State private var preview: [JSON] = []
  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      HStack {
        ColumnLabel("Source")
        TextField("Source label", text: $label)
        TextField("Sample paths (one per line)", text: $paths, axis: .vertical)
      }
      Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 8) {
        GridRow {
          ColumnLabel("Before")
          ColumnLabel("After")
          ColumnLabel("Action")
        }
        ForEach(preview, id: \.self) { r in
          Divider()
          GridRow {
            Text(r["rel"].text)
            Text(r["dest_rel"].text)
            StateLabel(word: r["action"].text)
          }
        }
      }

      if !message.isEmpty { Text(message).foregroundStyle(Palette.warn) }
    }.task(id: [row["id"].text, String(row["latest_revision"].count), label, paths]) {
      guard row["id"] != .null else { return }
      do {
        message = ""
        preview = []
        try await Task.sleep(for: .milliseconds(240))
        let result = try await store.rpc(
          "preset.preview",
          [
            "id": row["id"], "source_label": .string(label),
            "paths": .array(paths.split(separator: "\n").map { .string(String($0)) }),
          ])
        if !Task.isCancelled { preview = result.rows }
      } catch { if !Task.isCancelled { message = error.localizedDescription } }
    }
  }
}
