import CoreImage.CIFilterBuiltins
import FerryCore
import SwiftUI

/// Drives and paired machines as open lists on the room, each row with Path Radar's route.
struct Machines: View {
  @Bindable var store: Store
  @State private var scope = "All"
  @State private var pairing = false

  var body: some View {
    PageFrame {
      PageHead(title: "Machines & Drives") {
        Button {
          pairing = true
        } label: {
          Label("Pair machine", systemImage: "plus")
        }
        .buttonStyle(.primary)
      }
    } content: { width in
      VStack(alignment: .leading, spacing: Space.s7) {
        VStack(spacing: 0) {
          HStack(alignment: .bottom) {
            UnderlineTabs(
              choices: ["All", "Drives", "Peers"], selection: $scope,
              counts: ["Drives": store.drives.count, "Peers": store.peers.count])
            Spacer()
          }
          Hairline(color: Palette.rule2)
        }
        if scope != "Peers" {
          VStack(alignment: .leading, spacing: Space.s3) {
            SectionTitle("Drives", count: store.drives.count)
            VStack(spacing: 0) {
              ForEach(store.drives, id: \.self) { drive in
                Hairline()
                DriveRow(store: store, drive: drive)
              }
              Hairline()
            }
            if store.drives.isEmpty { Quiet("No mounted drives reported.") }
          }
        }
        if scope != "Drives" {
          VStack(alignment: .leading, spacing: Space.s3) {
            SectionTitle("Paired machines", count: store.peers.count)
            VStack(spacing: 0) {
              ForEach(store.peers, id: \.self) { peer in
                Hairline()
                PeerRow(store: store, peer: peer)
              }
              Hairline()
            }
            if store.peers.isEmpty { Quiet("No paired machines. Pair one to send over the LAN or Tailscale.") }
          }
        }
      }
    }
    .sheet(isPresented: $pairing) { PairSheet(store: store) }
  }
}

/// One mounted drive: what it is, where it is mounted, how much room is left, and the way to
/// offload it.
struct DriveRow: View {
  let store: Store
  let drive: JSON
  var compact = false

  var body: some View {
    let mount = drive["mount_path"].text
    HStack(spacing: Space.s4) {
      IconDisc(symbol: drive["card"] == .null ? "externaldrive" : "sdcard")
      VStack(alignment: .leading, spacing: 4) {
        Text(URL(fileURLWithPath: mount).lastPathComponent).font(.inter(TypeScale.base, .semibold))
          .foregroundStyle(Palette.ink1).lineLimit(1)
        Detail(text: mount)
      }
      .frame(maxWidth: .infinity, alignment: .leading)
      TransportBadge(text: driveKind(drive)).frame(width: 150, alignment: .leading)
      VStack(alignment: .trailing, spacing: 6) {
        Text(Format.bytes(drive["free_bytes"].count) + " free").font(.inter(TypeScale.sm))
          .foregroundStyle(Palette.ink2)
        if !compact {
          Meter(fraction: share, color: Palette.accent, height: 4).frame(width: 90)
        }
      }
      .frame(width: compact ? 90 : 110, alignment: .trailing)
      Button("Plan offload") {
        let source = Location(path: mount)
        if !store.sources.contains(source) { store.sources.append(source) }
        store.review = nil
        store.section = Place.transfer.rawValue
      }
      .buttonStyle(.park(.standard, .small))
      .frame(width: 112, alignment: .trailing)
    }
    .padding(.vertical, compact ? 10 : Space.s3)
  }

  /// Free space against the roomiest mounted drive.
  private var share: Double {
    let largest = store.drives.map { $0["free_bytes"].double }.max() ?? 0
    return drive["free_bytes"].double / max(1, largest)
  }
}

/// One paired machine: its name, the route Path Radar chose, and the ways to use it.
struct PeerRow: View {
  let store: Store
  let peer: JSON
  var compact = false
  @State private var sending = false
  @State private var revoking = false
  @State private var remotePath = ""
  @State private var measured: JSON?

  var body: some View {
    VStack(alignment: .leading, spacing: Space.s2) {
      HStack(spacing: Space.s4) {
        IconDisc(symbol: "desktopcomputer")
        VStack(alignment: .leading, spacing: 4) {
          Text(peer["name"].text).font(.inter(TypeScale.base, .semibold)).foregroundStyle(Palette.ink1)
            .lineLimit(1)
          Text(compact ? "Paired machine" : seen).font(.inter(TypeScale.xs))
            .foregroundStyle(Palette.ink3).lineLimit(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        TransportBadge(text: routeLabel(peer["active_path"])).frame(width: 150, alignment: .leading)
        Text(compact ? "" : peer["id"].text).font(.mono(11)).foregroundStyle(Palette.ink4)
          .lineLimit(1).truncationMode(.middle)
          .frame(width: compact ? 90 : 110, alignment: .trailing)
        HStack(spacing: Space.s1) {
          if !compact {
            MoreMenu(help: "More for \(peer["name"].text)") {
              Button("Probe routes") {
                store.perform {
                  measured = try await store.rpc("peer.probe", ["peer_id": peer["id"]])
                }
              }
              Button("Revoke access…") { revoking = true }
            }
          }
          Button("Send") {
            remotePath = ""
            sending = true
          }
          .buttonStyle(.park(.standard, .small))
        }
        .frame(width: 112, alignment: .trailing)
      }
      if let report = measured {
        VStack(alignment: .leading, spacing: 4) {
          Text(report["error"].text.isEmpty ? report["info"].text : report["error"].text)
            .font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
          ForEach(report["measurements"].rows, id: \.self) { m in
            Detail(
              text:
                "\(m["badge"].text) · \(m["addr"].text) · \(String(format: "%.1f", m["rtt"].double / 1_000_000)) ms · \(Format.bytes(Int64(m["bps"].double)))/s \(m["error"].text)"
            )
          }
        }
        .padding(.leading, 36 + Space.s4)
      }
    }
    .padding(.vertical, compact ? 10 : Space.s3)
    .help(peer["last_seen"].text.isEmpty ? "" : "Last seen " + peer["last_seen"].text)
    .sheet(isPresented: $sending) {
      SheetPage(title: "Send to " + peer["name"].text, height: 300) {
        FieldLabel("Destination path on \(peer["name"].text)")
        TextField("/Volumes/…", text: $remotePath)
        Button("Use destination") {
          let target = Location(path: remotePath, peerID: peer["id"].text, label: peer["name"].text)
          if !store.targets.contains(target) { store.targets.append(target) }
          store.review = nil
          sending = false
          store.section = Place.transfer.rawValue
        }
        .buttonStyle(.primary)
        .disabled(!remotePath.hasPrefix("/"))
      }
    }
    .confirmationDialog("Revoke this peer’s access?", isPresented: $revoking) {
      Button("Revoke " + peer["name"].text, role: .destructive) {
        store.perform {
          _ = try await store.rpc("peer.remove", ["peer_id": peer["id"]])
          try await store.refresh()
        }
      }
    }
  }

  private var seen: String {
    peer["last_seen"].text.isEmpty ? "Paired machine" : "Seen " + peer["last_seen"].text
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

func driveKind(_ drive: JSON) -> String {
  if drive["card"] != .null { return drive["card"]["vendor"].text + " card" }
  switch drive["class"].text {
  case "ssd": return "Local SSD"
  case "hdd": return "Local HDD"
  case "network": return "Network volume"
  case "removable": return "Removable"
  default: return "Local volume"
  }
}

func routeLabel(_ endpoint: JSON) -> String {
  let tier = endpoint["tier"].count
  let badge = endpoint["badge"].text
  guard tier > 0 else { return badge }
  let name = tier == 2 && badge == "LAN" ? "Wired LAN" : badge
  return ["Tier " + String(tier), name].filter { !$0.isEmpty }.joined(separator: " · ")
}
