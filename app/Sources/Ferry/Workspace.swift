import FerryCore
import SwiftUI

let policies = ["keep_both", "skip_identical", "needs_review"]
struct Workspace: View {
  @Bindable var store: Store
  @State private var busy = false
  var body: some View {
    Pipeline(active: busy ? 1 : 0)
    HStack(alignment: .top, spacing: 12) {
      Endpoints(store: store, source: true)
      Endpoints(store: store, source: false)
    }
    Panel(title: "Transfer settings") {
      HStack(alignment: .top, spacing: 22) {
        setting("Organization") {
          FerryMenu(
            title: store.presets.first { $0["id"].text == store.preset }?["name"].text
              ?? "Destination default"
          ) {
            Button("Destination default") { store.preset = "" }
            ForEach(store.presets, id: \.self) { row in
              Button(row["name"].text) { store.preset = row["id"].text }
            }
          }
        }
        setting("Conflict policy") {
          FerryMenu(title: display(store.policy)) {
            ForEach(policies, id: \.self) { p in Button(display(p)) { store.policy = p } }
          }
        }
        setting("Checksum") {
          Segments(
            choices: ["SHA-256", "BLAKE3"],
            selection: Binding(
              get: { store.checksum == "blake3" ? "BLAKE3" : "SHA-256" },
              set: { store.checksum = $0 == "BLAKE3" ? "blake3" : "sha256" }))
        }
      }
      Divider()
      ForEach(store.sources, id: \.self) { row in
        let label = row.label
        TextField(
          "Camera role for \(label) (optional)",
          text: Binding(
            get: { store.roles[label] ?? "" },
            set: { store.roles[label] = $0 }))
      }
    }
    PillButton(
      title: busy ? "Planning…" : "Build Plan", role: .primary,
      enabled: store.online && !busy && !store.sources.isEmpty && !store.targets.isEmpty
    ) {
      busy = true
      let request = PlanRequest(workspace: store)
      store.perform {
        defer { busy = false }
        store.review = try await store.rpc("plan.create", request.params)
      }
    }
  }
  func display(_ value: String) -> String {
    value.replacingOccurrences(of: "_", with: " ").capitalized
  }
  func setting<C: View>(_ title: String, @ViewBuilder content: () -> C) -> some View {
    VStack(alignment: .leading, spacing: 7) {
      ColumnLabel(title)
      content()
    }
  }
}

struct Pipeline: View {
  let active: Int
  let stages = ["Sources", "Plan", "Review Tree", "Approve", "Execute"]
  var body: some View {
    HStack(spacing: 0) {
      ForEach(stages.indices, id: \.self) { i in
        HStack(spacing: 6) {
          Text(String(format: "%02d", i + 1)).font(.system(size: 10, design: .monospaced))
          Text(stages[i]).font(.inter(12, .semibold))
        }.foregroundStyle(i == active ? Palette.onAccent : Palette.ink3)
          .frame(maxWidth: .infinity).frame(height: 32)
          .background(Capsule().fill(i == active ? Palette.accent : .clear))
      }
    }.padding(3).background(Palette.rail, in: Capsule()).overlay(
      Capsule().strokeBorder(Palette.rim))
  }
}

struct Endpoints: View {
  let store: Store
  let source: Bool
  @State private var targeted = false
  @State private var binding: JSON?
  @State private var peer: JSON?
  @State private var path = ""
  var entries: [Location] { source ? store.sources : store.targets }
  func add(_ row: Location) {
    guard !entries.contains(row) else { return }
    if source { store.sources.append(row) } else { store.targets.append(row) }
  }
  func folder(_ path: String, peerID: JSON? = nil) {
    guard !path.isEmpty else { return }
    add(Location(path: path, peerID: peerID?.text))
  }
  var body: some View {
    Panel(title: source ? "Sources" : "Destinations", glow: targeted ? Palette.brand : nil) {
      Text(
        source ? "Drop camera cards or folders here." : "Drop a folder or choose a paired machine."
      ).font(.inter(12)).foregroundStyle(Palette.ink3)
      ForEach(entries, id: \.self) { row in
        let saved = store.destinations.first { $0["id"].text == row.destination_id }
        let bindingPath = saved?["resolution"]["binding"].text ?? ""
        let path = row.path ?? (bindingPath.isEmpty ? saved?["root_path"].text ?? "" : bindingPath)
        let peerID = row.peer_id ?? saved?["peer_id"].text ?? ""
        let local = peerID.isEmpty || peerID == "local"
        let drive =
          local
          ? store.drives.filter {
            let mount = $0["mount_path"].text
            return mount == path || path.hasPrefix(mount == "/" ? "/" : mount + "/")
          }.max { $0["mount_path"].text.count < $1["mount_path"].text.count } : nil
        HStack {
          VStack(alignment: .leading, spacing: 7) {
            Label(
              row.label,
              systemImage: !local
                ? "desktopcomputer"
                : (drive?["card"] ?? .null) != .null ? "sdcard" : "externaldrive"
            )
            .font(.inter(17, .semibold))
            Detail(text: path)
            if let drive {
              TransportBadge(text: driveKind(drive))
              FreeSpaceMeter(drive: drive, drives: store.drives)
            } else if let peer = store.peers.first(where: { $0["id"].text == peerID }) {
              TransportBadge(text: routeLabel(peer["active_path"]))
            }
          }
          Button {
            if source {
              store.sources.removeAll { $0 == row }
            } else {
              store.targets.removeAll { $0 == row }
            }
          } label: {
            Image(systemName: "minus.circle")
          }.buttonStyle(.plain).help("Remove " + row.label).accessibilityLabel(
            "Remove " + row.label)
        }.padding(12).background(Palette.rail.opacity(0.72), in: RoundedRectangle(cornerRadius: 7))
          .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Palette.rule2))
      }
      FerryMenu(title: source ? "Add source" : "Add destination") {
        Button("Choose folders…") { for url in pickFolders() { folder(url.path) } }
        ForEach(store.drives, id: \.self) { row in
          Button(row["mount_path"].text) { folder(row["mount_path"].text) }
        }
        if !source {
          ForEach(store.destinations, id: \.self) { row in
            let state = row["resolution"]["state"].text
            Button(row["name"].text + " · " + state) {
              if state == "needs_confirmation" {
                binding = row
              } else {
                add(Location(destinationID: row["id"].text, label: row["name"].text))
              }
            }.disabled(!["available", "needs_confirmation", "remote"].contains(state))
          }
        }
        ForEach(store.peers, id: \.self) { row in
          Button("\(row["name"].text)…") {
            path = ""
            peer = row
          }
        }
      }.fixedSize()
    }.confirmationDialog(
      "Use this destination binding?",
      isPresented: Binding(get: { binding != nil }, set: { if !$0 { binding = nil } }),
      presenting: binding
    ) { row in
      if let candidate = row["resolution"]["candidates"].rows.first,
        row["resolution"]["candidates"].rows.count == 1
      {
        Button("Use " + candidate.text) {
          add(Location(destinationID: row["id"].text, label: row["name"].text, confirmed: true))
        }
      }
    } message: { row in
      Text(row["resolution"]["reason"].text)
    }.dropDestination(for: URL.self) { urls, _ in
      let files = urls.filter(\.isFileURL)
      for url in files { folder(url.path) }
      return !files.isEmpty
    } isTargeted: {
      targeted = $0
    }.sheet(isPresented: Binding(get: { peer != nil }, set: { if !$0 { peer = nil } })) {
      SheetPage(title: peer?["name"].text ?? "") {
        TextField("Absolute path on peer", text: $path)
        PillButton(title: "Add path", role: .primary, enabled: path.hasPrefix("/")) {
          folder(path, peerID: peer?["id"])
          peer = nil
        }
      }
    }
  }
}

struct FreeSpaceMeter: View {
  let drive: JSON
  let drives: [JSON]
  var body: some View {
    HStack {
      let largest = drives.map { $0["free_bytes"].double }.max() ?? 0
      GeometryReader { proxy in
        Capsule().fill(Palette.rim).overlay(alignment: .leading) {
          Capsule().fill(Palette.accent).frame(
            width: proxy.size.width * max(0, min(1, drive["free_bytes"].double / max(1, largest))))
        }
      }.frame(width: 55, height: 4)
      Text(Format.bytes(drive["free_bytes"].count) + " free").font(.inter(11)).foregroundStyle(
        Palette.ink3)
    }.help("Free space relative to the largest available mounted drive")
  }
}
