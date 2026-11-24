import CoreImage.CIFilterBuiltins
import FerryCore
import SwiftUI
import UniformTypeIdentifiers

/// The two halves of Devices.
enum DevicesTab: String, CaseIterable {
  case drives = "Drives & machines"
  case places = "Destinations & layouts"
}

private struct Editor: Identifiable {
  enum Kind { case preset, destination, camera }
  let kind: Kind
  var row: JSON = .null
  let id = UUID()
}

/// Setting up where things live: the drives and machines Ferry can reach, and the saved
/// destinations and folder layouts a copy can use. Lists sit open on the room; nothing here
/// needs glass.
struct Devices: View {
  @Bindable var store: Store
  @Bindable var chrome: Chrome
  @State private var pairing = false
  @State private var editor: Editor?

  private var tab: DevicesTab { DevicesTab(rawValue: chrome.devicesTab) ?? .drives }

  var body: some View {
    PageFrame {
      PageHead(title: "Devices") {
        switch tab {
        case .drives:
          Button {
            pairing = true
          } label: {
            Label("Pair machine", systemImage: "plus")
          }
          .buttonStyle(.primary)
        case .places:
          Button {
            editor = Editor(kind: .destination)
          } label: {
            Label("New destination", systemImage: "plus")
          }
          .buttonStyle(.primary)
        }
      }
    } content: { _ in
      VStack(alignment: .leading, spacing: Space.s7) {
        VStack(spacing: 0) {
          HStack(alignment: .bottom) {
            UnderlineTabs(choices: DevicesTab.allCases.map(\.rawValue), selection: $chrome.devicesTab)
            Spacer()
          }
          Hairline(color: Palette.rule2)
        }
        switch tab {
        case .drives:
          drives
          machines
        case .places:
          destinations
          layouts
        }
      }
    }
    .sheet(isPresented: $pairing) { PairSheet(store: store) }
    .sheet(item: $editor) { editor in
      switch editor.kind {
      case .destination: DestinationEditor(store: store, row: editor.row)
      case .preset: PresetEditor(store: store, row: editor.row)
      case .camera: CameraEditor(store: store)
      }
    }
  }

  // MARK: Drives & machines

  private var drives: some View {
    VStack(alignment: .leading, spacing: Space.s3) {
      SectionTitle("Drives", count: store.drives.count)
      Rows(store.drives) { drive in
        let mount = drive["mount_path"].text
        DeviceRow(
          symbol: drive["card"] == .null ? "externaldrive" : "sdcard",
          title: URL(fileURLWithPath: mount).lastPathComponent,
          detail: "\(driveKind(drive)) · \(Format.bytes(drive["free_bytes"].count)) free",
          note: mount
        ) {
          Button {
            let source = Location(path: mount)
            if !store.sources.contains(source) { store.sources.append(source) }
            store.review = nil
            store.section = Place.transfer.rawValue
          } label: {
            Label("Copy from", systemImage: "arrow.right").labelStyle(TrailingIcon())
          }
          .buttonStyle(.park(.quiet, .small))
        }
      }
      if store.drives.isEmpty { Quiet("Plug in a camera card or drive and it appears here.") }
    }
  }

  private var machines: some View {
    VStack(alignment: .leading, spacing: Space.s3) {
      SectionTitle("Paired machines", count: store.peers.count)
      Rows(store.peers) { peer in PeerRow(store: store, peer: peer) }
      if store.peers.isEmpty {
        Quiet("Pair another Mac or Linux machine to copy to it over your network or Tailscale.")
      }
    }
  }

  // MARK: Destinations & layouts

  private var destinations: some View {
    VStack(alignment: .leading, spacing: Space.s3) {
      SectionTitle("Saved destinations", count: store.destinations.count)
      Rows(store.destinations) { row in
        let resolution = row["resolution"]
        DeviceRow(
          symbol: row["peer_id"].text.isEmpty ? "archivebox" : "desktopcomputer",
          title: row["name"].text,
          detail: resolution["reason"].text.nilIfEmpty ?? stateWords(resolution["state"].text),
          note: row["root_path"].text
        ) {
          MoreMenu(help: "Manage \(row["name"].text)") {
            Button("Check the drive") {
              store.perform {
                let updated = try await store.rpc("dest.resolve", ["id": row["id"]])
                if let index = store.destinations.firstIndex(where: { $0["id"] == row["id"] }) {
                  store.destinations[index] = updated
                }
              }
            }
            Button("Edit…") { editor = Editor(kind: .destination, row: row) }
            Button("Move to another folder…") {
              if let path = pickFolders(multiple: false).first?.path {
                mutate("dest.rebind", ["id": row["id"], "path": .string(path)])
              }
            }
            Divider()
            Button("Archive") { mutate("dest.archive", ["id": row["id"]]) }
          }
        }
      }
      if store.destinations.isEmpty {
        Quiet("Save a destination to land every copy in the same place.")
      }
    }
  }

  private var layouts: some View {
    VStack(alignment: .leading, spacing: Space.s3) {
      SectionTitle(title: "Folder layouts", count: store.presets.count) {
        HStack(spacing: Space.s1) {
          Button("Import…") {
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
          Button("Name a camera…") { editor = Editor(kind: .camera) }
            .buttonStyle(.park(.quiet, .small))
          Button("New layout") { editor = Editor(kind: .preset) }
            .buttonStyle(.park(.standard, .small))
        }
      }
      Text("How files are arranged when they land: by camera, by day, or as they were on the card.")
        .font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink3)
      Rows(store.presets) { row in
        DeviceRow(
          symbol: "square.stack.3d.up", title: row["name"].text,
          detail: "Revision \(row["latest_revision"].count)", note: ""
        ) {
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
      }
    }
  }

  private func stateWords(_ state: String) -> String {
    switch state {
    case "available": "Ready"
    case "remote": "On a paired machine"
    case "needs_confirmation": "Confirm which drive it is on"
    case "unavailable": "Drive not connected"
    default: display(state)
    }
  }

  func mutate(_ method: String, _ params: JSON) {
    store.perform {
      _ = try await store.rpc(method, params)
      try await store.refresh()
    }
  }
}

/// Rows split by hairlines, a hairline above the first and below the last.
struct Rows<Item: Hashable, Row: View>: View {
  let items: [Item]
  @ViewBuilder var row: (Item) -> Row

  init(_ items: [Item], @ViewBuilder row: @escaping (Item) -> Row) {
    self.items = items
    self.row = row
  }

  var body: some View {
    VStack(spacing: 0) {
      ForEach(items, id: \.self) { item in
        Hairline()
        row(item)
      }
      if !items.isEmpty { Hairline() }
    }
  }
}

/// One thing Ferry knows about: its glyph, its name and a line of plain words, a quiet mono
/// note (a path), and its actions at the trailing edge.
struct DeviceRow<Actions: View>: View {
  let symbol: String
  let title: String
  let detail: String
  let note: String
  @ViewBuilder var actions: Actions

  var body: some View {
    HStack(spacing: Space.s4) {
      IconDisc(symbol: symbol, size: 34)
      VStack(alignment: .leading, spacing: 3) {
        Text(title).font(.inter(TypeScale.base, .semibold)).foregroundStyle(Palette.ink1).lineLimit(1)
        Text(detail).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3).lineLimit(1)
      }
      .layoutPriority(1)
      Spacer(minLength: Space.s4)
      if !note.isEmpty {
        Text(note).font(.mono(11)).foregroundStyle(Palette.ink4).lineLimit(1).truncationMode(.middle)
      }
      HStack(spacing: Space.s1) { actions }.fixedSize()
    }
    .padding(.vertical, 11)
  }
}

/// One paired machine: its route in plain words, and the ways to use it.
struct PeerRow: View {
  let store: Store
  let peer: JSON
  @State private var sending = false
  @State private var revoking = false
  @State private var remotePath = ""
  @State private var measured: JSON?
  @State private var probing = false

  var body: some View {
    VStack(alignment: .leading, spacing: Space.s2) {
      DeviceRow(symbol: "desktopcomputer", title: peer["name"].text, detail: line, note: "") {
        Button(probing ? "Checking…" : "Check speeds") {
          probing = true
          store.perform {
            defer { probing = false }
            measured = try await store.rpc("peer.probe", ["peer_id": peer["id"]])
          }
        }
        .buttonStyle(.park(.quiet, .small))
        .disabled(probing)
        Button {
          remotePath = ""
          sending = true
        } label: {
          Label("Copy to", systemImage: "arrow.right").labelStyle(TrailingIcon())
        }
        .buttonStyle(.park(.quiet, .small))
        MoreMenu(help: "More for \(peer["name"].text)") {
          Button("Unpair…") { revoking = true }
        }
      }
      if let report = measured {
        VStack(alignment: .leading, spacing: 4) {
          if !(report["error"].text.isEmpty && report["info"].text.isEmpty) {
            Text(report["error"].text.isEmpty ? report["info"].text : report["error"].text)
              .font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
          }
          ForEach(report["measurements"].rows, id: \.self) { m in
            Detail(
              text:
                "\(m["badge"].text) · \(m["addr"].text) · \(String(format: "%.1f", m["rtt"].double / 1_000_000)) ms · \(Format.bytes(Int64(m["bps"].double)))/s \(m["error"].text)"
            )
          }
        }
        .padding(.leading, 34 + Space.s4)
        .padding(.bottom, Space.s2)
      }
    }
    .sheet(isPresented: $sending) {
      PeerPathSheet(peer: peer, verb: "Copy to", path: $remotePath) {
        let target = Location(path: remotePath, peerID: peer["id"].text, label: peer["name"].text)
        if !store.targets.contains(target) { store.targets.append(target) }
        store.review = nil
        sending = false
        store.section = Place.transfer.rawValue
      }
    }
    .confirmationDialog("Unpair \(peer["name"].text)?", isPresented: $revoking) {
      Button("Unpair " + peer["name"].text, role: .destructive) {
        store.perform {
          _ = try await store.rpc("peer.remove", ["peer_id": peer["id"]])
          try await store.refresh()
        }
      }
    } message: {
      Text("It will no longer be able to send to or receive from this Mac until you pair again.")
    }
  }

  private var line: String {
    let seen = Stamp.day(peer["last_seen"].text)
    return [routeName(peer["active_path"]), seen.isEmpty ? "" : "seen \(seen)"]
      .filter { !$0.isEmpty }.joined(separator: " · ")
  }
}

struct PairSheet: View {
  let store: Store
  @State private var join = false
  @State private var code = ""
  @State private var expires = ""
  @State private var pasted = ""
  @State private var message = ""
  @Environment(\.dismiss) var dismiss

  var body: some View {
    SheetPage(title: "Pair machine", height: 600) {
      Segments(
        choices: ["Invite", "Join"],
        selection: Binding(get: { join ? "Join" : "Invite" }, set: { join = $0 == "Join" }))
      if join {
        Text("Paste the invite the other machine shows. It works once, and only while it lasts.")
          .font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink3)
        TextField("ferry://pair/…", text: $pasted)
        Button("Join machine") {
          Task {
            do {
              _ = try await store.rpc("peer.pair.complete", ["code": .string(pasted)])
              try await store.refresh()
              dismiss()
            } catch { message = error.localizedDescription }
          }
        }
        .buttonStyle(.primary)
        .disabled(!pasted.hasPrefix("ferry://pair/"))
      } else {
        Text("Create a single-use invite, then join it from the other machine.")
          .font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink3)
        Button("Create invite") {
          Task {
            do {
              let invite = try await store.rpc("peer.pair.start")
              code = invite["code"].text
              expires = invite["expires_at"].text
            } catch { message = error.localizedDescription }
          }
        }
        .buttonStyle(.primary)
        if !code.isEmpty {
          HStack(alignment: .top, spacing: Space.s5) {
            if let image = qr {
              Image(nsImage: image).interpolation(.none).resizable()
                .frame(width: 180, height: 180)
                .padding(12)
                .background(RoundedRectangle(cornerRadius: 16, style: .continuous).fill(.white))
            }
            VStack(alignment: .leading, spacing: Space.s3) {
              Text(code).font(.mono(12)).foregroundStyle(Palette.ink1).textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
              TimelineView(.periodic(from: .now, by: 1)) { context in
                let date =
                  ISO8601DateFormatter().date(from: expires)
                  ?? (try? Date(expires, strategy: .iso8601.time(includingFractionalSeconds: true)))
                Text(
                  date.map { "Expires in " + Format.eta(max(0, Int64($0.timeIntervalSince(context.date)))) }
                    ?? "Expires " + expires
                )
                .font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
              }
              Button {
                copyText(code)
              } label: {
                Label("Copy invite", systemImage: "doc.on.doc")
              }
              .buttonStyle(.park(.standard, .small))
            }
          }
          .padding(Space.s5)
          .background(GlassSurface(radius: 20))
        }
      }
      if !message.isEmpty { Text(message).foregroundStyle(Palette.warn) }
    }
  }

  var qr: NSImage? {
    let filter = CIFilter.qrCodeGenerator()
    filter.message = Data(code.utf8)
    guard let image = filter.outputImage,
      let cg = CIContext().createCGImage(image, from: image.extent)
    else { return nil }
    return NSImage(cgImage: cg, size: image.extent.size)
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
