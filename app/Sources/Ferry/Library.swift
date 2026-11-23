import FerryCore
import SwiftUI
import UniformTypeIdentifiers

private struct Editor: Identifiable {
  enum Kind { case preset, destination, camera }
  let kind: Kind
  var row: JSON = .null
  let id = UUID()
}

/// Saved destinations as raised glass cards, organization presets as an open list, and a
/// glass pane that previews where a preset would put things.
struct Library: View {
  @Bindable var store: Store
  @State private var editor: Editor?
  @State private var previewID = ""
  @State private var scope = "All"

  var body: some View {
    PageFrame {
      PageHead(title: "Presets & Destinations")
    } content: { width in
      VStack(alignment: .leading, spacing: Space.s7) {
        VStack(spacing: 0) {
          HStack(alignment: .bottom) {
            UnderlineTabs(
              choices: ["All", "Destinations", "Presets"], selection: $scope,
              counts: ["Destinations": store.destinations.count, "Presets": store.presets.count])
            Spacer()
          }
          Hairline(color: Palette.rule2)
        }
        if scope != "Presets" { destinations(width) }
        if scope != "Destinations" {
          presets
          if let row = store.presets.first(where: { $0["id"].text == previewID }) ?? store.presets.first {
            preview(row)
          }
        }
      }
    }
    .sheet(item: $editor) { editor in
      switch editor.kind {
      case .destination: DestinationEditor(store: store, row: editor.row)
      case .preset: PresetEditor(store: store, row: editor.row)
      case .camera: CameraEditor(store: store)
      }
    }
  }

  // MARK: Destinations

  private func destinations(_ width: CGFloat) -> some View {
    VStack(alignment: .leading, spacing: Space.s4) {
      SectionTitle(title: "Saved destinations", count: store.destinations.count) {
        Button {
          editor = Editor(kind: .destination)
        } label: {
          Label("New destination", systemImage: "plus")
        }
        .buttonStyle(.park(.standard, .small))
      }
      LazyVGrid(
        columns: Array(
          repeating: GridItem(.flexible(), spacing: Space.s4, alignment: .top),
          count: width >= 900 ? 3 : 2),
        alignment: .leading, spacing: Space.s4
      ) {
        ForEach(store.destinations, id: \.self) { row in destinationCard(row) }
      }
      if store.destinations.isEmpty {
        Quiet("Save a destination to land offloads in the same place every time.")
      }
    }
  }

  private func destinationCard(_ row: JSON) -> some View {
    let resolution = row["resolution"]
    return VStack(alignment: .leading, spacing: Space.s4) {
      HStack(alignment: .top, spacing: Space.s3) {
        IconDisc(symbol: row["peer_id"].text.isEmpty ? "externaldrive" : "desktopcomputer", size: 42)
        VStack(alignment: .leading, spacing: 4) {
          Text(row["name"].text).font(.inter(TypeScale.md, .semibold)).foregroundStyle(Palette.bone)
            .lineLimit(1)
          Detail(text: row["root_path"].text)
        }
        Spacer(minLength: 0)
        MoreMenu(help: "Manage \(row["name"].text)") {
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
          Divider()
          Button("Archive") { mutate("dest.archive", ["id": row["id"]]) }
        }
      }
      HStack(spacing: Space.s2) {
        StatusPill(word: resolution["state"].text)
        if !resolution["reason"].text.isEmpty {
          Text(resolution["reason"].text).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
            .lineLimit(2)
        }
      }
    }
    .padding(Space.s5)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(GlassSurface(radius: 20))
  }

  // MARK: Presets

  private var presets: some View {
    VStack(alignment: .leading, spacing: Space.s3) {
      SectionTitle(title: "Organization presets", count: store.presets.count) {
        HStack(spacing: Space.s2) {
          Button("Import JSON…") {
            let panel = NSOpenPanel()
            panel.allowedContentTypes = [.json]
            guard panel.runModal() == .OK, let url = panel.url else { return }
            store.perform {
              let doc = try JSONDecoder().decode(JSON.self, from: Data(contentsOf: url))
              _ = try await store.rpc("preset.import", ["json": doc])
              try await store.refresh()
            }
          }
          .buttonStyle(.park(.quiet, .small))
          Button("Bind camera…") { editor = Editor(kind: .camera) }
            .buttonStyle(.park(.standard, .small))
          Button {
            editor = Editor(kind: .preset)
          } label: {
            Label("New preset", systemImage: "plus")
          }
          .buttonStyle(.park(.primary, .small))
        }
      }
      VStack(spacing: 0) {
        ForEach(store.presets, id: \.self) { row in
          Hairline()
          HStack(spacing: Space.s4) {
            IconDisc(symbol: "square.stack.3d.up")
            VStack(alignment: .leading, spacing: 4) {
              Text(row["name"].text).font(.inter(TypeScale.base, .semibold)).foregroundStyle(Palette.ink1)
              Text("Revision \(row["latest_revision"].count)").font(.inter(TypeScale.xs))
                .foregroundStyle(Palette.ink3)
            }
            Spacer()
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
            .buttonStyle(.park(.quiet, .small))
            Button("Open") { editor = Editor(kind: .preset, row: row) }
              .buttonStyle(.park(.standard, .small))
          }
          .padding(.vertical, Space.s3)
        }
        Hairline()
      }
      if store.presets.isEmpty { Quiet("No presets yet.") }
    }
  }

  private func preview(_ row: JSON) -> some View {
    VStack(alignment: .leading, spacing: Space.s4) {
      HStack(alignment: .center, spacing: Space.s3) {
        CardTitle("Preview a saved revision")
        Spacer()
        GlassMenu(title: row["name"].text, icon: "square.stack.3d.up", size: .small) {
          ForEach(store.presets, id: \.self) { p in
            Button(p["name"].text) { previewID = p["id"].text }
          }
        }
      }
      PresetPreview(store: store, row: row)
    }
    .padding(Space.s6)
    .background(GlassSurface(radius: 20))
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
    SheetPage(title: "Saved destination", height: 380) {
      FieldLabel("Name")
      TextField("Studio archive", text: $name)
      FieldLabel("Folder")
      HStack(spacing: Space.s2) {
        TextField("/Volumes/…", text: $path)
        Button("Choose…") { if let url = pickFolders(multiple: false).first { path = url.path } }
          .buttonStyle(.standard)
      }
      Button("Save destination") {
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
      .buttonStyle(.primary)
      .disabled(name.isEmpty || !path.hasPrefix("/"))
      if !message.isEmpty { Text(message).foregroundStyle(Palette.warn) }
    }
    .onAppear {
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
    SheetPage(title: "Bind camera role", height: 460) {
      ForEach(keys, id: \.self) { key in
        FieldLabel(key.capitalized)
        TextField(key.capitalized, text: Binding(get: { fields[key] ?? "" }, set: { fields[key] = $0 }))
      }
      Button("Save binding") {
        Task {
          do {
            _ = try await store.rpc("camera.bind", .object(fields.mapValues(JSON.string)))
            dismiss()
          } catch { message = error.localizedDescription }
        }
      }
      .buttonStyle(.primary)
      .disabled(!keys.allSatisfy { !(fields[$0] ?? "").isEmpty })
      if !message.isEmpty { Text(message).foregroundStyle(Palette.warn) }
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
    SheetPage(title: row["name"].text.isEmpty ? "New preset" : row["name"].text, width: 640, height: 680) {
      FieldLabel("Rules, groups & exclusions")
      TextEditor(text: $json).font(.mono(12)).scrollContentBackground(.hidden)
        .padding(Space.s3).frame(height: 240).parkField(radius: 14)
      Button(row["builtin"] == .bool(true) ? "Save as new preset" : "Save revision") {
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
      .buttonStyle(.primary)
      .disabled(json.isEmpty)
      if row["id"] != .null {
        Hairline()
        PresetPreview(store: store, row: row)
      }
      if !message.isEmpty { Text(message).foregroundStyle(Palette.warn) }
    }
    .task {
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

/// Sample paths through a preset's saved revision: before, after and what would happen.
private struct PresetPreview: View {
  let store: Store
  let row: JSON
  @State private var paths = "DCIM/Clip001.mov"
  @State private var label = "Camera A"
  @State private var message = ""
  @State private var preview: [JSON] = []

  var body: some View {
    VStack(alignment: .leading, spacing: Space.s4) {
      HStack(alignment: .top, spacing: Space.s3) {
        VStack(alignment: .leading, spacing: Space.s2) {
          FieldLabel("Source label")
          TextField("Source label", text: $label).frame(width: 180)
        }
        VStack(alignment: .leading, spacing: Space.s2) {
          FieldLabel("Sample paths, one per line")
          TextField("Sample paths (one per line)", text: $paths, axis: .vertical)
        }
      }
      VStack(spacing: 0) {
        HStack {
          FieldLabel("Before").frame(maxWidth: .infinity, alignment: .leading)
          FieldLabel("After").frame(maxWidth: .infinity, alignment: .leading)
          FieldLabel("Action").frame(width: 110, alignment: .trailing)
        }
        .padding(.bottom, Space.s2)
        ForEach(preview, id: \.self) { r in
          Hairline()
          HStack {
            Detail(text: r["rel"].text, color: Palette.ink2).frame(maxWidth: .infinity, alignment: .leading)
            HStack(spacing: 6) {
              Image(systemName: "arrow.right").font(.system(size: 8, weight: .bold)).foregroundStyle(Palette.accent)
              Detail(text: r["dest_rel"].text, color: Palette.ink1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            StatusPill(word: r["action"].text).frame(width: 110, alignment: .trailing)
          }
          .padding(.vertical, 10)
        }
      }
      if !message.isEmpty { Text(message).foregroundStyle(Palette.warn) }
    }
    .task(id: [row["id"].text, String(row["latest_revision"].count), label, paths]) {
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
