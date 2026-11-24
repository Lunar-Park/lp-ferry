import FerryCore
import SwiftUI

let policies = ["keep_both", "skip_identical", "needs_review"]

/// The words a person reads for a conflict policy.
func policyName(_ policy: String) -> String {
  switch policy {
  case "keep_both": "Keep both"
  case "skip_identical": "Skip identical"
  case "needs_review": "Ask me"
  default: display(policy)
  }
}

/// Ferry's home: what is happening now (only when something is), then one card that asks
/// two questions, what to copy and where it goes, and one button to review the copy.
struct TransferPage: View {
  @Bindable var store: Store
  @Bindable var chrome: Chrome
  @State private var busy = false

  var body: some View {
    PageFrame {
      PageHead(title: "Transfer") { StageMarker(reviewing: false) }
    } content: { width in
      VStack(alignment: .leading, spacing: Space.s5) {
        LiveBanner(store: store, chrome: chrome)
        builder(width)
      }
    }
  }

  private var ready: Bool { !store.sources.isEmpty && !store.targets.isEmpty }

  private func builder(_ width: CGFloat) -> some View {
    VStack(alignment: .leading, spacing: 0) {
      Group {
        if width >= 620 {
          HStack(alignment: .top, spacing: 0) {
            FromColumn(store: store).frame(maxWidth: .infinity, alignment: .topLeading)
            Crossing().padding(.horizontal, Space.s5)
            ToColumn(store: store).frame(maxWidth: .infinity, alignment: .topLeading)
          }
        } else {
          VStack(alignment: .leading, spacing: Space.s6) {
            FromColumn(store: store)
            ToColumn(store: store)
          }
        }
      }
      .padding(Space.s6)
      Hairline()
      OptionsRow(store: store, open: $chrome.optionsOpen)
        .padding(.horizontal, Space.s6).padding(.vertical, Space.s4)
      Hairline()
      HStack(spacing: Space.s4) {
        Text(summary).font(.inter(TypeScale.sm)).foregroundStyle(ready ? Palette.ink2 : Palette.ink3)
          .lineLimit(1).truncationMode(.middle)
        Spacer(minLength: Space.s4)
        Button {
          busy = true
          let request = PlanRequest(workspace: store)
          store.perform {
            defer { busy = false }
            store.review = try await store.rpc("plan.create", request.params)
          }
        } label: {
          Label(busy ? "Reading the card…" : "Review transfer", systemImage: "arrow.right")
            .labelStyle(TrailingIcon())
        }
        .buttonStyle(.primary)
        .keyboardShortcut(.defaultAction)
        .disabled(!store.online || busy || !ready)
      }
      .padding(.horizontal, Space.s6).padding(.vertical, Space.s4)
    }
    .background(GlassSurface(radius: 22))
  }

  private var summary: String {
    guard ready else {
      return store.sources.isEmpty
        ? "Pick what to copy, then where it should go." : "Now pick where it should go."
    }
    let from = store.sources.map(\.label).joined(separator: ", ")
    let to = store.targets.map(\.label).joined(separator: ", ")
    return "\(from) → \(to)"
  }
}

/// The two stages, quietly in the head band: set up, then review and copy.
struct StageMarker: View {
  let reviewing: Bool

  var body: some View {
    HStack(spacing: Space.s2) {
      stage("Set up", on: !reviewing, done: reviewing)
      Image(systemName: "chevron.right").font(.system(size: 9, weight: .bold)).foregroundStyle(Palette.ink4)
      stage("Review & copy", on: reviewing, done: false)
    }
    .fixedSize()
    .accessibilityElement(children: .ignore)
    .accessibilityLabel(reviewing ? "Step 2 of 2: review and copy" : "Step 1 of 2: set up")
  }

  private func stage(_ word: String, on: Bool, done: Bool) -> some View {
    HStack(spacing: 5) {
      if done {
        Image(systemName: "checkmark").font(.system(size: 9, weight: .bold)).foregroundStyle(Palette.accent)
      }
      Text(word).font(.inter(13, on ? .semibold : .medium))
        .foregroundStyle(on ? Palette.bone : done ? Palette.ink2 : Palette.ink4)
    }
  }
}

/// A label with its icon after the words (Review transfer →).
struct TrailingIcon: LabelStyle {
  func makeBody(configuration: Configuration) -> some View {
    HStack(spacing: 6) {
      configuration.title
      configuration.icon.font(.system(size: 11, weight: .bold))
    }
  }
}

/// The seam between From and To: a hairline with the crossing arrow on it.
private struct Crossing: View {
  var body: some View {
    VStack(spacing: 0) {
      Rectangle().fill(Palette.rule1).frame(width: 1).frame(maxHeight: .infinity)
      Image(systemName: "arrow.right").font(.system(size: 12, weight: .semibold))
        .foregroundStyle(Palette.accent)
        .frame(width: 30, height: 30)
        .background(Circle().fill(Palette.accent.opacity(0.12)))
        .padding(.vertical, Space.s2)
      Rectangle().fill(Palette.rule1).frame(width: 1).frame(maxHeight: .infinity)
    }
    .frame(width: 30)
  }
}

// MARK: - From and To

/// One thing you can pick, picked with one click: a ring that fills with a tick, its name and
/// a line saying what it is.
struct PickRow: View {
  let symbol: String
  let title: String
  let detail: String
  let selected: Bool
  var removable = false
  let action: () -> Void
  @State private var hovering = false

  var body: some View {
    Button(action: action) {
      HStack(spacing: Space.s3) {
        ZStack {
          if selected {
            Circle().fill(Palette.accent)
            Image(systemName: "checkmark").font(.system(size: 9, weight: .heavy))
              .foregroundStyle(Palette.groundDeep)
          } else {
            Circle().strokeBorder(hovering ? Palette.ink3 : Palette.ghost, lineWidth: 1.5)
          }
        }
        .frame(width: 18, height: 18)
        Image(systemName: symbol).font(.system(size: 15)).foregroundStyle(selected ? Palette.accentInk : Palette.accent)
          .frame(width: 22)
        VStack(alignment: .leading, spacing: 3) {
          Text(title).font(.inter(TypeScale.base, .semibold))
            .foregroundStyle(selected ? Palette.bone : Palette.ink1).lineLimit(1)
          Text(detail).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3).lineLimit(1)
            .truncationMode(.middle)
        }
        Spacer(minLength: Space.s2)
        if removable && hovering {
          Image(systemName: "xmark").font(.system(size: 9, weight: .bold)).foregroundStyle(Palette.ink3)
        }
      }
      .padding(.horizontal, Space.s3).frame(height: 54)
      .background(
        RoundedRectangle(cornerRadius: 14, style: .continuous)
          .fill(selected ? Palette.accent.opacity(hovering ? 0.17 : 0.13) : Color.white.opacity(hovering ? 0.045 : 0))
      )
      .overlay(
        RoundedRectangle(cornerRadius: 14, style: .continuous)
          .strokeBorder(selected ? Palette.accent.opacity(0.35) : .clear, lineWidth: 1)
      )
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
    .onHover { hovering = $0 }
    .accessibilityLabel("\(title), \(detail)")
    .accessibilityAddTraits(selected ? .isSelected : [])
  }
}

/// A column's head: the question it asks and a line of help.
private struct ColumnHead: View {
  let title: String
  let help: String
  var body: some View {
    VStack(alignment: .leading, spacing: 4) {
      Text(title).font(.inter(TypeScale.lg, .semibold)).tracking(-0.3).foregroundStyle(Palette.bone)
      Text(help).font(.inter(13)).foregroundStyle(Palette.ink3).lineLimit(1)
    }
    .padding(.bottom, Space.s2)
  }
}

/// The sunk row at the foot of a column: drop a folder here, or choose one.
private struct DropRow<Items: View>: View {
  let prompt: String
  let drop: ([URL]) -> Void
  @ViewBuilder var items: Items
  @State private var targeted = false

  var body: some View {
    HStack(spacing: Space.s3) {
      Image(systemName: "plus").font(.system(size: 13, weight: .medium))
        .foregroundStyle(targeted ? Palette.accent : Palette.ink3).frame(width: 18)
      Text(prompt).font(.inter(13)).foregroundStyle(targeted ? Palette.ink1 : Palette.ink3).lineLimit(1)
      Spacer(minLength: Space.s2)
      GlassMenu(title: "Choose", size: .small) { items }
    }
    .padding(.leading, Space.s3).padding(.trailing, 6)
    .frame(height: 44)
    .parkField(radius: 14)
    .overlay {
      if targeted {
        RoundedRectangle(cornerRadius: 14, style: .continuous)
          .strokeBorder(Palette.accent.opacity(0.8), lineWidth: 1.5).allowsHitTesting(false)
      }
    }
    .padding(.top, Space.s1)
    .dropDestination(for: URL.self) { urls, _ in
      let folders = urls.filter(\.isFileURL)
      drop(folders)
      return !folders.isEmpty
    } isTargeted: {
      targeted = $0
    }
  }
}

/// What to copy: every mounted card and drive, one click each, then any folder.
struct FromColumn: View {
  @Bindable var store: Store
  @State private var peer: JSON?
  @State private var path = ""

  private var drives: [JSON] {
    store.drives.sorted { ($0["card"] != .null ? 0 : 1) < ($1["card"] != .null ? 0 : 1) }
  }

  var body: some View {
    VStack(alignment: .leading, spacing: Space.s1) {
      ColumnHead(title: "From", help: "Cards, drives or folders")
      ForEach(drives, id: \.self) { drive in
        let mount = drive["mount_path"].text
        let location = Location(path: mount)
        PickRow(
          symbol: drive["card"] == .null ? "externaldrive" : "sdcard",
          title: location.label,
          detail: "\(driveKind(drive)) · \(Format.bytes(drive["free_bytes"].count)) free",
          selected: store.sources.contains { $0.path == mount && $0.peer_id == nil }
        ) {
          if store.sources.contains(where: { $0.path == mount && $0.peer_id == nil }) {
            store.sources.removeAll { $0.path == mount && $0.peer_id == nil }
          } else {
            store.sources.append(location)
          }
        }
      }
      ForEach(extra, id: \.self) { row in
        PickRow(
          symbol: row.peer_id == nil ? "folder" : "desktopcomputer", title: row.label,
          detail: where_(row), selected: true, removable: true
        ) { store.sources.removeAll { $0 == row } }
      }
      if store.drives.isEmpty && extra.isEmpty {
        Text("Plug in a camera card or drive and it appears here.")
          .font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink3).padding(.vertical, Space.s3)
      }
      DropRow(prompt: "Drop a folder", drop: { $0.forEach { add($0.path) } }) {
        Button("Folder on this Mac…") { pickFolders().forEach { add($0.path) } }
        if !store.peers.isEmpty {
          Section("On a paired machine") {
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
    .sheet(isPresented: Binding(get: { peer != nil }, set: { if !$0 { peer = nil } })) {
      PeerPathSheet(peer: peer ?? .null, verb: "Copy from", path: $path) {
        if let peer {
          let row = Location(path: path, peerID: peer["id"].text)
          if !store.sources.contains(row) { store.sources.append(row) }
        }
        peer = nil
      }
    }
  }

  /// Sources that are not one of the mounted drives.
  private var extra: [Location] {
    let mounts = Set(store.drives.map { $0["mount_path"].text })
    return store.sources.filter { $0.peer_id != nil || !mounts.contains($0.path ?? "") }
  }

  private func where_(_ row: Location) -> String {
    if let peerID = row.peer_id {
      let name = store.peers.first { $0["id"].text == peerID }?["name"].text ?? "a paired machine"
      return "On \(name) · \(row.path ?? "")"
    }
    return row.path ?? ""
  }

  private func add(_ path: String) {
    let row = Location(path: path)
    if !store.sources.contains(row) { store.sources.append(row) }
  }
}

/// Where it goes: saved destinations, other drives, paired machines, then any folder.
struct ToColumn: View {
  @Bindable var store: Store
  @State private var peer: JSON?
  @State private var path = ""
  @State private var binding: JSON?

  var body: some View {
    VStack(alignment: .leading, spacing: Space.s1) {
      ColumnHead(title: "To", help: "Destinations, drives or machines")
      ForEach(saved, id: \.self) { row in
        let id = row["id"].text
        PickRow(
          symbol: row["peer_id"].text.isEmpty ? "archivebox" : "desktopcomputer",
          title: row["name"].text, detail: savedDetail(row),
          selected: store.targets.contains { $0.destination_id == id }
        ) {
          if store.targets.contains(where: { $0.destination_id == id }) {
            store.targets.removeAll { $0.destination_id == id }
          } else if row["resolution"]["state"] == "needs_confirmation" {
            binding = row
          } else {
            store.targets.append(Location(destinationID: id, label: row["name"].text))
          }
        }
      }
      ForEach(drives, id: \.self) { drive in
        let mount = drive["mount_path"].text
        PickRow(
          symbol: "externaldrive", title: URL(fileURLWithPath: mount).lastPathComponent,
          detail: "\(driveKind(drive)) · \(Format.bytes(drive["free_bytes"].count)) free",
          selected: store.targets.contains { $0.path == mount && $0.peer_id == nil }
        ) {
          if store.targets.contains(where: { $0.path == mount && $0.peer_id == nil }) {
            store.targets.removeAll { $0.path == mount && $0.peer_id == nil }
          } else {
            store.targets.append(Location(path: mount))
          }
        }
      }
      ForEach(store.peers, id: \.self) { row in
        let id = row["id"].text
        let chosen = store.targets.first { $0.peer_id == id && $0.destination_id == nil }
        PickRow(
          symbol: "desktopcomputer", title: row["name"].text,
          detail: chosen.map { "Into \($0.path ?? "")" } ?? routeName(row["active_path"]),
          selected: chosen != nil
        ) {
          if chosen != nil {
            store.targets.removeAll { $0.peer_id == id && $0.destination_id == nil }
          } else {
            path = ""
            peer = row
          }
        }
      }
      ForEach(extra, id: \.self) { row in
        PickRow(symbol: "folder", title: row.label, detail: row.path ?? "", selected: true, removable: true) {
          store.targets.removeAll { $0 == row }
        }
      }
      DropRow(prompt: "Drop a folder", drop: { $0.forEach { add($0.path) } }) {
        Button("Folder on this Mac…") { pickFolders().forEach { add($0.path) } }
      }
    }
    .sheet(isPresented: Binding(get: { peer != nil }, set: { if !$0 { peer = nil } })) {
      PeerPathSheet(peer: peer ?? .null, verb: "Copy to", path: $path) {
        if let peer {
          let row = Location(path: path, peerID: peer["id"].text, label: peer["name"].text)
          if !store.targets.contains(row) { store.targets.append(row) }
        }
        peer = nil
      }
    }
    .confirmationDialog(
      "Is this the right drive?",
      isPresented: Binding(get: { binding != nil }, set: { if !$0 { binding = nil } }),
      presenting: binding
    ) { row in
      if let candidate = row["resolution"]["candidates"].rows.first,
        row["resolution"]["candidates"].rows.count == 1
      {
        Button("Use " + candidate.text) {
          store.targets.append(
            Location(destinationID: row["id"].text, label: row["name"].text, confirmed: true))
        }
      }
    } message: { row in
      Text(row["resolution"]["reason"].text)
    }
  }

  private var saved: [JSON] {
    store.destinations.filter {
      ["available", "needs_confirmation", "remote"].contains($0["resolution"]["state"].text)
    }
  }

  /// Drives you could copy to: not a camera card, not something you are copying from, and
  /// not already home to a saved destination (that one is listed by name).
  private var drives: [JSON] {
    let homes = store.destinations.map { $0["root_path"].text }
    return store.drives.filter { drive in
      let mount = drive["mount_path"].text
      return drive["card"] == .null && !store.sources.contains { $0.path == mount }
        && !homes.contains { $0 == mount || $0.hasPrefix(mount + "/") }
    }
  }

  private var extra: [Location] {
    let mounts = Set(store.drives.map { $0["mount_path"].text })
    return store.targets.filter {
      $0.destination_id == nil && $0.peer_id == nil && !mounts.contains($0.path ?? "")
    }
  }

  private func savedDetail(_ row: JSON) -> String {
    let state = row["resolution"]["state"].text
    if let peerID = row["peer_id"].text.nilIfEmpty {
      let name = store.peers.first { $0["id"].text == peerID }?["name"].text ?? "a paired machine"
      return "Saved · on \(name)"
    }
    if state == "needs_confirmation" { return "Saved · confirm the drive" }
    let root = row["root_path"].text
    if let drive = store.drives.first(where: {
      let mount = $0["mount_path"].text
      return root == mount || root.hasPrefix(mount + "/")
    }) {
      return "Saved · \(Format.bytes(drive["free_bytes"].count)) free"
    }
    return "Saved · \(root)"
  }

  private func add(_ path: String) {
    let row = Location(path: path)
    if !store.targets.contains(row) { store.targets.append(row) }
  }
}

/// A path on a paired machine, asked for in a small sheet.
struct PeerPathSheet: View {
  let peer: JSON
  let verb: String
  @Binding var path: String
  let done: () -> Void

  var body: some View {
    SheetPage(title: "\(verb) \(peer["name"].text)", height: 300) {
      FieldLabel("The folder's full path on \(peer["name"].text)")
      TextField("/Volumes/…", text: $path).onSubmit { if path.hasPrefix("/") { done() } }
      Button("Use this folder", action: done)
        .buttonStyle(.primary)
        .disabled(!path.hasPrefix("/"))
    }
  }
}

// MARK: - Options

/// The copy's options in one calm line, opened only when someone wants them.
struct OptionsRow: View {
  @Bindable var store: Store
  @Binding var open: Bool

  private var presetName: String {
    store.presets.first { $0["id"].text == store.preset }?["name"].text ?? "Default folders"
  }

  var body: some View {
    VStack(alignment: .leading, spacing: Space.s4) {
      Button {
        withAnimation(.spring(response: 0.32, dampingFraction: 0.86)) { open.toggle() }
      } label: {
        HStack(spacing: Space.s3) {
          Image(systemName: "slider.horizontal.3").font(.system(size: 12, weight: .medium))
            .foregroundStyle(Palette.ink3)
          Text(
            [presetName, policyName(store.policy), store.checksum == "blake3" ? "BLAKE3" : "SHA-256"]
              .joined(separator: "  ·  ")
          )
          .font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink2).lineLimit(1)
          Spacer(minLength: Space.s3)
          HStack(spacing: 5) {
            Text("Options")
            Image(systemName: "chevron.down").font(.system(size: 9, weight: .bold))
              .rotationEffect(.degrees(open ? 180 : 0))
          }
          .font(.inter(13, .medium)).foregroundStyle(Palette.ink2)
        }
        .contentShape(Rectangle())
      }
      .buttonStyle(.plain)
      .accessibilityLabel(open ? "Hide options" : "Show options")
      if open {
        HStack(alignment: .top, spacing: Space.s7) {
          option("Folder layout") {
            GlassMenu(title: presetName, size: .small) {
              Button("Default folders") { store.preset = "" }
              ForEach(store.presets, id: \.self) { row in
                Button(row["name"].text) { store.preset = row["id"].text }
              }
            }
          }
          option("If a file is already there") {
            GlassMenu(title: policyName(store.policy), size: .small) {
              ForEach(policies, id: \.self) { p in Button(policyName(p)) { store.policy = p } }
            }
          }
          option("Check every file with") {
            Segments(
              choices: ["SHA-256", "BLAKE3"],
              selection: Binding(
                get: { store.checksum == "blake3" ? "BLAKE3" : "SHA-256" },
                set: { store.checksum = $0 == "BLAKE3" ? "blake3" : "sha256" }))
          }
          Spacer(minLength: 0)
        }
        if !store.sources.isEmpty {
          option("Camera names, used by folder layouts (optional)") {
            VStack(alignment: .leading, spacing: Space.s2) {
              ForEach(store.sources, id: \.self) { row in
                let label = row.label
                HStack(spacing: Space.s3) {
                  Text(label).font(.inter(TypeScale.sm, .medium)).foregroundStyle(Palette.ink2)
                    .frame(width: 110, alignment: .leading).lineLimit(1)
                  TextField(
                    "A camera, B camera…",
                    text: Binding(get: { store.roles[label] ?? "" }, set: { store.roles[label] = $0 })
                  )
                  .frame(maxWidth: 260)
                }
              }
            }
          }
        }
      }
    }
  }

  private func option<C: View>(_ title: String, @ViewBuilder content: () -> C) -> some View {
    VStack(alignment: .leading, spacing: Space.s2) {
      FieldLabel(title)
      content()
    }
  }
}

// MARK: - Live banner

/// What is happening now, in one compact strip: the copy running and any request waiting.
/// When nothing is, it takes no room at all.
struct LiveBanner: View {
  let store: Store
  let chrome: Chrome

  var body: some View {
    let running = store.progress.keys.sorted()
    let waiting = store.approvals.keys.sorted()
    if !running.isEmpty || !waiting.isEmpty {
      VStack(spacing: 0) {
        ForEach(Array(running.enumerated()), id: \.element) { index, id in
          if index > 0 { Hairline() }
          if let live = store.progress[id] { runningRow(id, live) }
        }
        ForEach(Array(waiting.enumerated()), id: \.element) { index, id in
          if index > 0 || !running.isEmpty { Hairline() }
          if let row = store.approvals[id] { waitingRow(id, row) }
        }
      }
      .background(GlassSurface(radius: 18))
      .task(id: running + waiting) {
        let plans = running.compactMap { id in store.transfers.first { $0["id"].text == id }?["plan_id"].text }
        await chrome.routes.load(plans + waiting, store: store)
      }
    }
  }

  private func runningRow(_ id: String, _ live: JSON) -> some View {
    let row = store.transfers.first { $0["id"].text == id } ?? ["id": .string(id)]
    let fraction = live["overall_fraction"].double
    let rate = live["targets"].rows.reduce(0) { $0 + $1["write_bps"].double }
    let eta = live["eta_seconds"] == .null ? -1 : Int64(ceil(live["eta_seconds"].double))
    return HStack(spacing: Space.s4) {
      Ring(fraction: fraction).frame(width: 30, height: 30)
      VStack(alignment: .leading, spacing: 3) {
        Text(chrome.routes.title(transfer: row, live: live)).font(.inter(TypeScale.base, .semibold))
          .foregroundStyle(Palette.bone).lineLimit(1)
        Text(
          "\(Int((fraction * 100).rounded()))%  ·  \(Format.bytes(Int64(rate)))/s  ·  \(Format.eta(eta)) left"
        )
        .font(.inter(13)).foregroundStyle(Palette.ink3).lineLimit(1)
      }
      Spacer(minLength: Space.s3)
      Button {
        chrome.open(id, in: store)
      } label: {
        Label("Open", systemImage: "arrow.right").labelStyle(TrailingIcon())
      }
      .buttonStyle(.park(.quiet, .small))
    }
    .padding(.horizontal, Space.s5).frame(height: 62)
  }

  private func waitingRow(_ id: String, _ row: JSON) -> some View {
    HStack(spacing: Space.s4) {
      Image(systemName: "hand.raised").font(.system(size: 13, weight: .semibold))
        .foregroundStyle(Palette.warn)
        .frame(width: 30, height: 30)
        .background(Circle().fill(Palette.warn.opacity(0.14)))
      VStack(alignment: .leading, spacing: 3) {
        Text(chrome.routes.sentence(request: row)).font(.inter(TypeScale.base, .semibold))
          .foregroundStyle(Palette.bone).lineLimit(1)
        Text(row["reasons"].rows.first?.text ?? "Waiting for your approval").font(.inter(13))
          .foregroundStyle(Palette.ink3).lineLimit(1)
      }
      Spacer(minLength: Space.s3)
      Button {
        store.perform {
          store.review = try await store.rpc("plan.get", ["plan_id": .string(id)])
          store.section = Place.transfer.rawValue
        }
      } label: {
        Label("Review", systemImage: "arrow.right").labelStyle(TrailingIcon())
      }
      .buttonStyle(.park(.standard, .small))
    }
    .padding(.horizontal, Space.s5).frame(height: 62)
  }
}

/// How far along, as a ring.
struct Ring: View {
  let fraction: Double
  var color = Palette.run

  var body: some View {
    ZStack {
      Circle().stroke(Color.white.opacity(0.10), lineWidth: 3)
      Circle().trim(from: 0, to: max(0.02, min(1, fraction)))
        .stroke(color, style: StrokeStyle(lineWidth: 3, lineCap: .round))
        .rotationEffect(.degrees(-90))
        .shadow(color: color.opacity(0.5), radius: 3)
    }
    .padding(1.5)
  }
}

func driveKind(_ drive: JSON) -> String {
  if drive["card"] != .null { return drive["card"]["vendor"].text + " card" }
  switch drive["class"].text {
  case "ssd": return "Local SSD"
  case "hdd": return "Local HDD"
  case "network": return "Network volume"
  case "removable": return "Removable drive"
  default: return "Local volume"
  }
}

/// A route in plain words: "Wired LAN", "Tailscale".
func routeName(_ endpoint: JSON) -> String {
  let badge = endpoint["badge"].text
  if endpoint["tier"].count == 2 && badge == "LAN" { return "Wired LAN" }
  return badge.isEmpty ? "Paired machine" : badge
}

extension String {
  var nilIfEmpty: String? { isEmpty ? nil : self }
}
