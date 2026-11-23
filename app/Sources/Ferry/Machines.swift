import CoreImage.CIFilterBuiltins
import FerryCore
import SwiftUI

struct Machines: View {
  let store: Store
  var overview = false
  @State private var scope = "All"
  @State private var pairing = false
  @State private var remotePath = ""
  @State private var revoke: JSON?
  @State private var sendPeer: JSON?
  @State private var measured = [String: JSON]()
  var body: some View {
    Panel(title: overview ? "Connected sources & peers" : "Devices & Path Radar") {
      if !overview { Segments(choices: ["All", "Drives", "Peers"], selection: $scope) }
      Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: overview ? 7 : 12) {
        GridRow {
          ColumnLabel("Source / peer").frame(maxWidth: .infinity, alignment: .leading)
          ColumnLabel("Transport")
          ColumnLabel("Free")
          Color.clear.frame(width: 1, height: 1)
        }
        if scope != "Peers" {
          ForEach(store.drives, id: \.self) { drive in
            Divider()
            GridRow {
              device(
                drive["card"] == .null ? "externaldrive" : "sdcard",
                URL(fileURLWithPath: drive["mount_path"].text).lastPathComponent,
                drive["mount_path"].text)
              TransportBadge(text: driveKind(drive))
              Text(Format.bytes(drive["free_bytes"].count)).font(.inter(12))
              PillButton(title: "Plan Offload") {
                let source = Location(path: drive["mount_path"].text)
                if !store.sources.contains(source) { store.sources.append(source) }
                store.review = nil
                store.section = "Transfer"
              }
            }
          }
        }
        if scope != "Drives" {
          ForEach(store.peers, id: \.self) { peer in
            Divider()
            GridRow {
              device(
                "desktopcomputer", peer["name"].text, overview ? "Paired machine" : peer["id"].text
              ).help("Last seen " + peer["last_seen"].text)
              TransportBadge(text: routeLabel(peer["active_path"]))
              Text("—").foregroundStyle(Palette.ink4)
              HStack {
                PillButton(title: "Send") {
                  remotePath = ""
                  sendPeer = peer
                }
                if !overview {
                  FerryMenu(title: "More") {
                    Button("Probe routes") {
                      store.perform {
                        measured[peer["id"].text] = try await store.rpc(
                          "peer.probe", ["peer_id": peer["id"]])
                      }
                    }
                    Button("Revoke access…") { revoke = peer }
                  }
                }
              }
            }
            if !overview, let report = measured[peer["id"].text] {
              Text(report["error"].text.isEmpty ? report["info"].text : report["error"].text)
              ForEach(report["measurements"].rows, id: \.self) { m in
                Detail(
                  text:
                    "\(m["badge"].text) · \(m["addr"].text) · \(String(format:"%.1f",m["rtt"].double / 1_000_000)) ms · \(Format.bytes(Int64(m["bps"].double)))/s \(m["error"].text)"
                )
              }
            }
          }
        }
      }
      if store.drives.isEmpty && scope != "Peers" { Text("No mounted drives reported") }
      if store.peers.isEmpty && scope != "Drives" { Text("No paired machines") }
      if !overview {
        Divider()
        PillButton(title: "Pair machine", role: .primary) { pairing = true }
      }
    }.sheet(isPresented: $pairing) { PairSheet(store: store) }
      .sheet(isPresented: Binding(get: { sendPeer != nil }, set: { if !$0 { sendPeer = nil } })) {
        SheetPage(title: "Send to " + (sendPeer?["name"].text ?? "")) {
          TextField("Destination path on peer", text: $remotePath)
          PillButton(title: "Use destination", role: .primary, enabled: remotePath.hasPrefix("/")) {
            if let peer = sendPeer {
              let target = Location(
                path: remotePath, peerID: peer["id"].text, label: peer["name"].text)
              if !store.targets.contains(target) { store.targets.append(target) }
            }
            store.review = nil
            sendPeer = nil
            store.section = "Transfer"
          }
        }
      }
      .confirmationDialog(
        "Revoke this peer’s access?",
        isPresented: Binding(get: { revoke != nil }, set: { if !$0 { revoke = nil } }),
        presenting: revoke
      ) { peer in
        Button("Revoke " + peer["name"].text, role: .destructive) {
          store.perform {
            _ = try await store.rpc("peer.remove", ["peer_id": peer["id"]])
            try await store.refresh()
          }
        }
      }
  }
  func device(_ symbol: String, _ name: String, _ subtitle: String) -> some View {
    HStack(spacing: 10) {
      Image(systemName: symbol).foregroundStyle(Palette.brand).frame(width: 30, height: 30)
        .background(Palette.rail, in: RoundedRectangle(cornerRadius: 7))
      VStack(alignment: .leading, spacing: 3) {
        Text(name).font(.inter(14, .semibold)).lineLimit(1)
        Text(subtitle).font(.inter(11)).foregroundStyle(Palette.ink3).lineLimit(1).truncationMode(
          .middle)
      }
    }
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
    SheetPage(title: "Pair machine") {
      Segments(
        choices: ["Invite", "Join"],
        selection: Binding(get: { join ? "Join" : "Invite" }, set: { join = $0 == "Join" }))
      if join {
        TextField("ferry://pair/…", text: $pasted)
        PillButton(
          title: "Join machine", role: .primary, enabled: pasted.hasPrefix("ferry://pair/")
        ) {
          Task {
            do {
              _ = try await store.rpc("peer.pair.complete", ["code": .string(pasted)])
              try await store.refresh()
              dismiss()
            } catch { message = error.localizedDescription }
          }
        }
      } else {
        PillButton(title: "Create invite", role: .primary) {
          Task {
            do {
              let invite = try await store.rpc("peer.pair.start")
              code = invite["code"].text
              expires = invite["expires_at"].text
            } catch { message = error.localizedDescription }
          }
        }
        if !code.isEmpty {
          if let image = qr {
            Image(nsImage: image).interpolation(.none).resizable().frame(
              width: image.size.width, height: image.size.height
            )
            .padding(16).background(.white)
          }
          Detail(text: code)
          Button("Copy invite") { copyText(code) }
          TimelineView(.periodic(from: .now, by: 1)) { ctx in
            let fmt = ISO8601DateFormatter()
            let date =
              fmt.date(from: expires)
              ?? (try? Date(expires, strategy: .iso8601.time(includingFractionalSeconds: true)))
            if let date {
              Text("Expires in " + Format.eta(max(0, Int64(date.timeIntervalSince(ctx.date)))))
            } else {
              Text("Expires " + expires)
            }
          }
        }
      }
      if !message.isEmpty { Text(message).foregroundStyle(Palette.warn) }
    }
  }
  var qr: NSImage? {
    let f = CIFilter.qrCodeGenerator()
    f.message = Data(code.utf8)
    guard let img = f.outputImage, let cg = CIContext().createCGImage(img, from: img.extent) else {
      return nil
    }
    let moduleScale = max(1, floor(260 / img.extent.width))
    return NSImage(
      cgImage: cg,
      size: CGSize(width: img.extent.width * moduleScale, height: img.extent.height * moduleScale))
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

struct TransportBadge: View {
  let text: String
  var body: some View {
    Text(text.isEmpty ? "Route unknown" : text).font(.inter(11, .medium)).foregroundStyle(
      Palette.brand
    )
    .padding(.horizontal, 7).padding(.vertical, 3).background(
      Palette.rail, in: RoundedRectangle(cornerRadius: 4))
  }
}
