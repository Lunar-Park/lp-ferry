import FerryCore
import SwiftUI

let policies = ["keep_both", "skip_identical", "needs_review"]

/// Where a transfer starts: what to carry, where to land it, and how to arrange it, then one
/// primary action to build the plan.
struct Workspace: View {
  @Bindable var store: Store
  @State private var busy = false

  var body: some View {
    PageFrame {
      PageHead(title: "Transfer") { Steps(active: busy ? 1 : 0) }
    } content: { width in
      VStack(alignment: .leading, spacing: Space.s7) {
        if width >= 760 {
          HStack(alignment: .top, spacing: Space.s7) {
            Endpoints(store: store, source: true)
            Endpoints(store: store, source: false)
          }
        } else {
          Endpoints(store: store, source: true)
          Endpoints(store: store, source: false)
        }
        Hairline()
        settings
        Hairline()
        HStack(spacing: Space.s4) {
          Text(summary).font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink3).lineLimit(2)
          Spacer(minLength: Space.s4)
          Button {
            busy = true
            let request = PlanRequest(workspace: store)
            store.perform {
              defer { busy = false }
              store.review = try await store.rpc("plan.create", request.params)
            }
          } label: {
            Label(busy ? "Planning…" : "Build plan", systemImage: "arrow.right")
              .labelStyle(TrailingIcon())
          }
          .buttonStyle(.primary)
          .disabled(!store.online || busy || store.sources.isEmpty || store.targets.isEmpty)
        }
      }
    }
  }

  private var presetName: String {
    store.presets.first { $0["id"].text == store.preset }?["name"].text ?? "Destination default"
  }

  private var summary: String {
    let s = store.sources.count
    let t = store.targets.count
    return
      "\(s) \(s == 1 ? "source" : "sources") → \(t) \(t == 1 ? "destination" : "destinations") · \(presetName) · \(store.checksum == "blake3" ? "BLAKE3" : "SHA-256")"
  }

  private var settings: some View {
    VStack(alignment: .leading, spacing: Space.s5) {
      SectionTitle("Arrangement")
      HStack(alignment: .top, spacing: Space.s7) {
        setting("Organization") {
          GlassMenu(title: presetName, icon: "square.stack.3d.up") {
            Button("Destination default") { store.preset = "" }
            ForEach(store.presets, id: \.self) { row in
              Button(row["name"].text) { store.preset = row["id"].text }
            }
          }
        }
        setting("Conflict policy") {
          GlassMenu(title: display(store.policy), icon: "arrow.triangle.branch") {
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
      if !store.sources.isEmpty {
        VStack(alignment: .leading, spacing: Space.s2) {
          FieldLabel("Camera roles")
          ForEach(store.sources, id: \.self) { row in
            let label = row.label
            HStack(spacing: Space.s3) {
              Text(label).font(.inter(TypeScale.sm, .medium)).foregroundStyle(Palette.ink2)
                .frame(width: 120, alignment: .leading).lineLimit(1)
              TextField(
                "Camera role (optional)",
                text: Binding(get: { store.roles[label] ?? "" }, set: { store.roles[label] = $0 })
              )
              .frame(maxWidth: 320)
            }
          }
        }
      }
    }
  }

  private func setting<C: View>(_ title: String, @ViewBuilder content: () -> C) -> some View {
    VStack(alignment: .leading, spacing: Space.s2) {
      FieldLabel(title)
      content()
    }
  }
}

/// A label with its icon after the words (Build plan →).
struct TrailingIcon: LabelStyle {
  func makeBody(configuration: Configuration) -> some View {
    HStack(spacing: 6) {
      configuration.title
      configuration.icon.font(.system(size: 11, weight: .bold))
    }
  }
}

/// The transfer's five stages, quietly, in the head band: the stage you are on lit, those
/// behind you ticked.
struct Steps: View {
  let active: Int
  static let stages = ["Sources", "Plan", "Review", "Approve", "Execute"]

  var body: some View {
    // Every stage named when the band has room; otherwise only the one you are on.
    ViewThatFits(in: .horizontal) {
      strip(named: true)
      strip(named: false)
    }
  }

  private func strip(named: Bool) -> some View {
    HStack(spacing: Space.s2) {
      ForEach(Self.stages.indices, id: \.self) { i in
        if i > 0 {
          Rectangle().fill(i <= active ? Palette.accent.opacity(0.5) : Palette.rule2)
            .frame(width: named ? 14 : 8, height: 1)
        }
        HStack(spacing: 6) {
          ZStack {
            if i < active {
              Circle().fill(Palette.accent.opacity(0.22))
              Image(systemName: "checkmark").font(.system(size: 8, weight: .bold))
                .foregroundStyle(Palette.accentInk)
            } else if i == active {
              Circle().fill(Palette.sel)
              Text("\(i + 1)").font(.inter(10, .bold)).foregroundStyle(Palette.accentDeep)
            } else {
              Circle().strokeBorder(Palette.rule3, lineWidth: 1)
              Text("\(i + 1)").font(.inter(10, .semibold)).foregroundStyle(Palette.ink4)
            }
          }
          .frame(width: 18, height: 18)
          if named || i == active {
            Text(Self.stages[i]).font(.inter(13, i == active ? .semibold : .medium))
              .foregroundStyle(i == active ? Palette.bone : i < active ? Palette.ink2 : Palette.ink4)
          }
        }
      }
    }
    .fixedSize()
    .accessibilityElement(children: .ignore)
    .accessibilityLabel("Stage \(active + 1) of 5: \(Self.stages[active])")
  }
}

/// Sources or destinations: a heading with the way to add one, each one raised on a glass card,
/// and a sunk drop field under them.
struct Endpoints: View {
  @Bindable var store: Store
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
    VStack(alignment: .leading, spacing: Space.s4) {
      SectionTitle(title: source ? "Sources" : "Destinations", count: entries.count) {
        addMenu
      }
      ForEach(entries, id: \.self) { row in card(row) }
      HStack(spacing: Space.s3) {
        Image(systemName: source ? "sdcard" : "tray.and.arrow.down").font(.system(size: 14))
          .foregroundStyle(targeted ? Palette.accent : Palette.ink4)
        Text(source ? "Drop camera cards or folders" : "Drop a folder, or add a paired machine")
          .font(.inter(TypeScale.sm)).foregroundStyle(targeted ? Palette.ink1 : Palette.ink3)
      }
      .frame(maxWidth: .infinity).frame(height: 56)
      .parkField(radius: 18)
      .overlay {
        if targeted {
          RoundedRectangle(cornerRadius: 18, style: .continuous)
            .strokeBorder(Palette.accent.opacity(0.8), lineWidth: 1.5).allowsHitTesting(false)
        }
      }
    }
    .frame(maxWidth: .infinity, alignment: .topLeading)
    .dropDestination(for: URL.self) { urls, _ in
      let files = urls.filter(\.isFileURL)
      for url in files { folder(url.path) }
      return !files.isEmpty
    } isTargeted: {
      targeted = $0
    }
    .confirmationDialog(
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
    }
    .sheet(isPresented: Binding(get: { peer != nil }, set: { if !$0 { peer = nil } })) {
      SheetPage(title: peer?["name"].text ?? "", height: 300) {
        FieldLabel("Absolute path on \(peer?["name"].text ?? "the peer")")
        TextField("/Volumes/…", text: $path)
        Button("Add path") {
          folder(path, peerID: peer?["id"])
          peer = nil
        }
        .buttonStyle(.primary)
        .disabled(!path.hasPrefix("/"))
      }
    }
  }

  private var addMenu: some View {
    GlassMenu(title: source ? "Add source" : "Add destination", icon: "plus", size: .small) {
      Button("Choose folders…") { for url in pickFolders() { folder(url.path) } }
      if !store.drives.isEmpty {
        Section("Mounted drives") {
          ForEach(store.drives, id: \.self) { row in
            Button(row["mount_path"].text) { folder(row["mount_path"].text) }
          }
        }
      }
      if !source && !store.destinations.isEmpty {
        Section("Saved destinations") {
          ForEach(store.destinations, id: \.self) { row in
            let state = row["resolution"]["state"].text
            Button(row["name"].text + " · " + state) {
              if state == "needs_confirmation" {
                binding = row
              } else {
                add(Location(destinationID: row["id"].text, label: row["name"].text))
              }
            }
            .disabled(!["available", "needs_confirmation", "remote"].contains(state))
          }
        }
      }
      if !store.peers.isEmpty {
        Section("Paired machines") {
          ForEach(store.peers, id: \.self) { row in
            Button("\(row["name"].text)…") {
              path = ""
              peer = row
            }
          }
        }
      }
    }
  }

  private func card(_ row: Location) -> some View {
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
    let largest = store.drives.map { $0["free_bytes"].double }.max() ?? 0
    return VStack(alignment: .leading, spacing: Space.s4) {
      HStack(spacing: Space.s3) {
        IconDisc(
          symbol: !local
            ? "desktopcomputer" : (drive?["card"] ?? .null) != .null ? "sdcard" : "externaldrive",
          size: 38)
        VStack(alignment: .leading, spacing: 4) {
          Text(row.label).font(.inter(TypeScale.md, .semibold)).foregroundStyle(Palette.bone)
            .lineLimit(1)
          Detail(text: path)
        }
        Spacer(minLength: Space.s2)
        Button {
          if source {
            store.sources.removeAll { $0 == row }
          } else {
            store.targets.removeAll { $0 == row }
          }
        } label: {
          Image(systemName: "xmark").font(.system(size: 10, weight: .bold))
        }
        .buttonStyle(.park(.quiet, .small, disc: true))
        .help("Remove " + row.label)
        .accessibilityLabel("Remove " + row.label)
      }
      HStack(spacing: Space.s3) {
        if let drive {
          TransportBadge(text: driveKind(drive))
          Spacer(minLength: Space.s2)
          Meter(fraction: drive["free_bytes"].double / max(1, largest), color: Palette.accent, height: 4)
            .frame(width: 70)
            .help("Free space relative to the largest mounted drive")
          Text(Format.bytes(drive["free_bytes"].count) + " free").font(.inter(TypeScale.xs))
            .foregroundStyle(Palette.ink3)
        } else if let peer = store.peers.first(where: { $0["id"].text == peerID }) {
          TransportBadge(text: routeLabel(peer["active_path"]))
          Spacer(minLength: 0)
        } else if saved != nil {
          StatusPill(word: saved?["resolution"]["state"].text ?? "")
          Spacer(minLength: 0)
        }
      }
    }
    .padding(Space.s5)
    .background(GlassSurface(radius: 20))
  }
}
