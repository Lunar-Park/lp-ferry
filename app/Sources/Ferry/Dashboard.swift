import FerryCore
import SwiftUI

/// The dashboard, read the way Park's Desk reads: what is crossing now on one raised pane,
/// the places it can cross between as an open list, and at the side what needs you.
struct Dashboard: View {
  @Bindable var store: Store
  let chrome: Chrome

  private var attention: Int {
    store.approvals.count
      + store.transfers.filter { ["partial", "needs_attention"].contains($0["status"].text) }.count
  }

  private var note: String {
    var parts = [String]()
    if !store.progress.isEmpty { parts.append("\(store.progress.count) crossing") }
    if attention > 0 { parts.append("\(attention) \(attention == 1 ? "needs" : "need") you") }
    return parts.isEmpty ? "All quiet" : parts.joined(separator: " · ")
  }

  var body: some View {
    PageFrame {
      PageHead(title: "Dashboard", note: note)
    } content: { width in
      Columns(width: width, side: 300) {
        VStack(alignment: .leading, spacing: Space.s6 + 4) {
          crossing
          places
          DropField(store: store)
        }
      } side: {
        VStack(alignment: .leading, spacing: Space.s6) {
          yourMove
          Hairline()
          recent
        }
      }
    }
  }

  // MARK: Now crossing

  @ViewBuilder private var crossing: some View {
    VStack(alignment: .leading, spacing: Space.s4) {
      SectionTitle(title: "Now crossing") {
        if store.progress.isEmpty {
          ArrowLink(title: "Start a transfer") { store.section = Place.transfer.rawValue }
        }
      }
      if store.progress.isEmpty {
        Quiet("Nothing is crossing right now.")
      }
      ForEach(store.progress.keys.sorted(), id: \.self) { id in
        if let live = store.progress[id] {
          CrossingCard(store: store, id: id, live: live) {
            chrome.activityFilter = "All"
            store.section = Place.activity.rawValue
          }
        }
      }
    }
  }

  // MARK: Sources & peers

  private var places: some View {
    VStack(alignment: .leading, spacing: Space.s3) {
      SectionTitle(title: "Sources & peers") {
        ArrowLink(title: "Machines & Drives") { store.section = Place.machines.rawValue }
      }
      VStack(spacing: 0) {
        ForEach(store.drives, id: \.self) { drive in
          Hairline()
          DriveRow(store: store, drive: drive, compact: true)
        }
        ForEach(store.peers, id: \.self) { peer in
          Hairline()
          PeerRow(store: store, peer: peer, compact: true)
        }
        Hairline()
      }
      if store.drives.isEmpty && store.peers.isEmpty {
        Quiet("No drives mounted and no machines paired yet.")
      }
    }
  }

  // MARK: Your move

  private var yourMove: some View {
    VStack(alignment: .leading, spacing: Space.s4) {
      SectionTitle("Your move", count: attention)
      if attention == 0 { Quiet("Nothing is waiting on you.") }
      ForEach(store.approvals.keys.sorted(), id: \.self) { id in
        if let row = store.approvals[id] {
          ApprovalCard(store: store, id: id, row: row, compact: true)
        }
      }
      ForEach(
        store.transfers.filter { ["partial", "needs_attention"].contains($0["status"].text) },
        id: \.self
      ) { row in
        VStack(alignment: .leading, spacing: 6) {
          Text(row["id"].text).font(.inter(TypeScale.md, .semibold)).foregroundStyle(Palette.bone)
          Text("Stopped before every file landed.").font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink2)
          ArrowLink(title: "Look in Activity") {
            chrome.activityFilter = "Attention"
            store.section = Place.activity.rawValue
          }
        }
      }
    }
  }

  // MARK: Recent transfers

  private var recent: some View {
    VStack(alignment: .leading, spacing: Space.s3) {
      SectionTitle(title: "Recent transfers") {
        ArrowLink(title: "All") {
          chrome.activityFilter = "All"
          store.section = Place.activity.rawValue
        }
      }
      VStack(spacing: 0) {
        ForEach(Array(store.transfers.prefix(5).enumerated()), id: \.offset) { index, row in
          if index > 0 { Hairline() }
          let live = store.progress[row["id"].text]
          HStack(alignment: .center, spacing: Space.s3) {
            VStack(alignment: .leading, spacing: 4) {
              Text(row["id"].text).font(.inter(TypeScale.base, .semibold)).foregroundStyle(Palette.ink1)
                .lineLimit(1).truncationMode(.middle)
              Text(recentLine(row, live: live)).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
                .lineLimit(1)
            }
            Spacer(minLength: Space.s2)
            StatusPill(word: row["status"].text)
          }
          .padding(.vertical, Space.s3)
        }
      }
      if store.transfers.isEmpty { Quiet("Transfer history is empty.") }
    }
  }

  private func recentLine(_ row: JSON, live: JSON?) -> String {
    if let live, let target = live["targets"].rows.first {
      let rate = live["targets"].rows.reduce(0) { $0 + $1["write_bps"].double }
      return "\(Format.bytes(Int64(rate)))/s · \(target["done_files"].count) of \(target["total_files"].count) files"
    }
    let actor = row["actor"].text.replacingOccurrences(of: "agent:", with: "")
    return [actor, Stamp.day(row["started_at"].text)].filter { !$0.isEmpty }.joined(separator: " · ")
  }
}

/// A main column and a side column split by a hairline, or one over the other when narrow.
struct Columns<Main: View, Side: View>: View {
  let width: CGFloat
  var side: CGFloat = 300
  @ViewBuilder var main: Main
  @ViewBuilder var sideContent: Side

  init(
    width: CGFloat, side: CGFloat = 300, @ViewBuilder main: () -> Main,
    @ViewBuilder side sideContent: () -> Side
  ) {
    self.width = width
    self.side = side
    self.main = main()
    self.sideContent = sideContent()
  }

  var body: some View {
    if width >= 860 {
      HStack(alignment: .top, spacing: 0) {
        main.frame(maxWidth: .infinity, alignment: .topLeading)
        Rectangle().fill(Palette.rule1).frame(width: 1).padding(.horizontal, Space.s7)
        sideContent.frame(width: side, alignment: .topLeading)
      }
    } else {
      VStack(alignment: .leading, spacing: Space.s7) {
        main
        Hairline()
        sideContent
      }
    }
  }
}

/// The transfer that is crossing now, raised on glass: how far, how fast, what is landing.
struct CrossingCard: View {
  let store: Store
  let id: String
  let live: JSON
  let open: () -> Void

  var body: some View {
    let targets = live["targets"].rows
    let first = targets.first ?? .null
    let fraction = live["overall_fraction"].double
    VStack(alignment: .leading, spacing: Space.s4 + 2) {
      HStack(alignment: .top, spacing: Space.s3) {
        VStack(alignment: .leading, spacing: 5) {
          HStack(spacing: Space.s2) {
            Text(first["label"].text.isEmpty ? id : first["label"].text)
              .font(.inter(TypeScale.lg, .semibold)).foregroundStyle(Palette.bone)
            if !first["transport_badge"].text.isEmpty {
              TransportBadge(text: first["transport_badge"].text)
            }
          }
          Text(
            [id, first["total_files"] == .null
              ? "" : "\(first["done_files"].count) of \(first["total_files"].count) files"]
              .filter { !$0.isEmpty }.joined(separator: " · ")
          )
          .font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink3)
        }
        Spacer(minLength: Space.s2)
        StatusPill(word: "running")
      }
      HStack(alignment: .firstTextBaseline, spacing: Space.s6) {
        HStack(alignment: .firstTextBaseline, spacing: 2) {
          Text("\(Int((fraction * 100).rounded()))").font(.inter(44, .semibold)).tracking(-1.2)
          Text("%").font(.inter(TypeScale.lg, .medium)).foregroundStyle(Palette.ink3)
        }
        .foregroundStyle(Palette.bone)
        figure(
          Format.eta(live["eta_seconds"] == .null ? -1 : Int64(ceil(live["eta_seconds"].double))),
          "left")
        Spacer(minLength: 0)
        figure(Format.bytes(Int64(targets.reduce(0) { $0 + $1["write_bps"].double })) + "/s", "write")
        figure(Format.bytes(Int64(targets.reduce(0) { $0 + $1["verify_bps"].double })) + "/s", "verify")
      }
      VStack(spacing: Space.s3) {
        ForEach(targets, id: \.self) { target in
          HStack(spacing: Space.s6) {
          let total = max(1, target["total_bytes"].double)
          track(
            "Written", target["bytes_written"].count, total: target["total_bytes"].count,
            fraction: target["bytes_written"].double / total, color: Palette.run)
          track(
            "Read back & verified", target["bytes_verified"].count,
            total: target["total_bytes"].count, fraction: target["bytes_verified"].double / total,
            color: Palette.ok)
          }
        }
      }
      HStack(spacing: Space.s3) {
        Image(systemName: "film").font(.system(size: 11)).foregroundStyle(Palette.ink4)
        Detail(text: first["current_file"].text)
        Spacer(minLength: Space.s3)
        Button("Details", action: open).buttonStyle(.park(.quiet, .small))
        Button("Cancel") {
          store.perform {
            _ = try await store.rpc("transfer.cancel", ["transfer_id": .string(id)])
            try await store.refresh()
          }
        }
        .buttonStyle(.park(.destructive, .small))
      }
    }
    .padding(Space.s6)
    .background(GlassSurface(radius: 20, tint: Palette.accent.opacity(0.55)))
  }

  private func figure(_ value: String, _ label: String) -> some View {
    VStack(alignment: .leading, spacing: 2) {
      Text(value).font(.inter(TypeScale.md, .semibold)).foregroundStyle(Palette.ink1)
      Text(label).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
    }
  }

  private func track(_ title: String, _ bytes: Int64, total: Int64, fraction: Double, color: Color)
    -> some View
  {
    VStack(alignment: .leading, spacing: 6) {
      HStack {
        Text(title).foregroundStyle(Palette.ink2)
        Spacer()
        Text("\(Format.bytes(bytes)) of \(Format.bytes(total))").foregroundStyle(Palette.ink3)
      }
      .font(.inter(TypeScale.xs, .medium))
      Meter(fraction: fraction, color: color)
    }
  }
}

/// An agent's request waiting on a person, raised on glass.
struct ApprovalCard: View {
  let store: Store
  let id: String
  let row: JSON
  var compact = false
  var dismiss: (() -> Void)? = nil

  private var actor: String {
    let who = row["actor"].text.replacingOccurrences(of: "agent:", with: "")
    return who.isEmpty ? "An agent" : who
  }

  var body: some View {
    VStack(alignment: .leading, spacing: compact ? Space.s3 : Space.s4) {
      HStack(spacing: Space.s2) {
        Text(String(actor.prefix(1))).font(.inter(13, .semibold)).foregroundStyle(Palette.groundDeep)
          .frame(width: 26, height: 26).background(Circle().fill(Palette.sel))
        Text(actor).font(.inter(TypeScale.sm, .medium)).foregroundStyle(Palette.ink2)
        Spacer(minLength: 0)
        StatusPill(word: "needs approval")
      }
      Text(compact ? "Wants to start a transfer" : "\(actor) wants to start a transfer")
        .font(.inter(compact ? TypeScale.md : TypeScale.lg, .semibold)).foregroundStyle(Palette.bone)
        .fixedSize(horizontal: false, vertical: true)
      ForEach(row["reasons"].rows, id: \.self) { reason in
        Text(reason.text).font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink2)
          .fixedSize(horizontal: false, vertical: true)
      }
      if !compact {
        Detail(text: id)
      }
      HStack(spacing: Space.s3) {
        Button("Review plan") {
          store.perform {
            store.review = try await store.rpc("plan.get", ["plan_id": .string(id)])
            store.section = Place.transfer.rawValue
          }
        }
        .buttonStyle(.park(.primary, compact ? .small : .regular))
        if let dismiss {
          Button("Later", action: dismiss).buttonStyle(.park(.quiet, compact ? .small : .regular))
        }
      }
      .padding(.top, 2)
    }
    .padding(compact ? Space.s5 : Space.s6)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(GlassSurface(radius: 20))
  }
}

/// Park's capture bar, for cargo: drop a card or folder here, or choose one, to plan an offload.
struct DropField: View {
  let store: Store
  @State private var targeted = false

  var body: some View {
    HStack(spacing: Space.s3) {
      Image(systemName: "plus").font(.system(size: 15, weight: .regular)).foregroundStyle(Palette.ink2)
      Text("Drop a card or folder to plan an offload…").font(.inter(TypeScale.base))
        .foregroundStyle(Palette.ink3).lineLimit(1)
      Spacer(minLength: Space.s3)
      Button("Choose…") { for url in pickFolders() { add(url.path) } }
        .buttonStyle(.park(.standard, .small))
    }
    .padding(.leading, Space.s5).padding(.trailing, Space.s2)
    .frame(height: 50)
    .parkField(radius: 25)
    .overlay {
      if targeted {
        Capsule().strokeBorder(Palette.accent.opacity(0.8), lineWidth: 1.5).allowsHitTesting(false)
      }
    }
    .dropDestination(for: URL.self) { urls, _ in
      let folders = urls.filter(\.isFileURL)
      for url in folders { add(url.path) }
      return !folders.isEmpty
    } isTargeted: {
      targeted = $0
    }
  }

  private func add(_ path: String) {
    let source = Location(path: path)
    if !store.sources.contains(source) { store.sources.append(source) }
    store.review = nil
    store.section = Place.transfer.rawValue
  }
}

/// Dates as rows show them.
enum Stamp {
  static func day(_ iso: String) -> String {
    let formatter = ISO8601DateFormatter()
    guard let date = formatter.date(from: iso) else { return iso }
    return date.formatted(.dateTime.month(.abbreviated).day().hour().minute())
  }
}
