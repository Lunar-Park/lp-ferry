import CoreImage.CIFilterBuiltins
import FerryCore
import SwiftUI

struct Machines: View {
  let store: Store
  @State private var pairing = false
  @State private var remotePath = ""
  @State private var revoke: JSON?
  @State private var sendPeer: JSON?
  @State private var measured = [String: JSON]()
  var body: some View {
    Panel(title: "Mounted drives") {
      if store.drives.isEmpty { Text("No mounted drives reported") }
      ForEach(store.drives, id: \.self) { drive in
        let path = drive["mount_path"].text
        HStack {
          Image(systemName: drive["card"] != .null ? "sdcard" : "externaldrive").font(
            .system(size: 24))
          VStack(alignment: .leading, spacing: 4) {
            Text(URL(fileURLWithPath: path).lastPathComponent).font(.inter(17, .semibold))
            Detail(text: path)
            HStack {
              Text(drive["class"].text.uppercased())
              if drive["card"] != .null {
                Label(drive["card"]["vendor"].text + " card", systemImage: "sdcard").font(
                  .inter(12, .medium))
              }
            }
            let largest = store.drives.map { $0["free_bytes"].count }.max() ?? 0
            HStack {
              GeometryReader { proxy in
                Capsule().fill(Palette.rule2).overlay(alignment: .leading) {
                  Capsule().fill(Palette.ink4).frame(
                    width: proxy.size.width * drive["free_bytes"].double / Double(max(1, largest)))
                }
              }.frame(width: 80, height: 4).help(
                "Available bytes relative to the largest free space among mounted drives")
              Text(Format.bytes(drive["free_bytes"].count) + " free").font(.inter(12))
            }
          }
          Spacer()
          PillButton(title: "Plan Offload") {
            let source = Location(path: path)
            if !store.sources.contains(source) { store.sources.append(source) }
            store.review = nil
            store.section = "Transfer"
          }
        }
        Divider()
      }
    }
    Panel(title: "Path Radar") {
      if store.peers.isEmpty { Text("No paired machines").foregroundStyle(Palette.ink3) }
      ForEach(store.peers, id: \.self) { peer in
        Text(peer["name"].text).font(.inter(18, .semibold))
        Detail(
          text: [peer["active_path"]["badge"].text, peer["last_seen"].text, peer["id"].text].filter
          { !$0.isEmpty }.joined(separator: " · "))
        HStack {
          PillButton(title: "Probe routes") {
            store.perform {
              measured[peer["id"].text] = try await store.rpc("peer.probe", ["peer_id": peer["id"]])
            }
          }
          PillButton(title: "Send to machine") {
            remotePath = ""
            sendPeer = peer
          }
          PillButton(title: "Revoke", role: .destructive) { revoke = peer }
        }
        if let report = measured[peer["id"].text] {
          Text(report["error"].text.isEmpty ? report["info"].text : report["error"].text)
          ForEach(report["measurements"].rows, id: \.self) { m in
            Detail(
              text:
                "\(m["badge"].text) · \(m["addr"].text) · \(String(format: "%.1f", m["rtt"].double / 1_000_000)) ms · \(Format.bytes(Int64(m["bps"].double)))/s \(m["error"].text)"
            )
          }
        }
        Divider()
      }
      PillButton(title: "Pair machine", role: .primary) { pairing = true }
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
      Picker("Pairing", selection: $join) {
        Text("Invite").tag(false)
        Text("Join").tag(true)
      }.pickerStyle(.segmented)
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
            Image(nsImage: image).interpolation(.none).resizable().frame(width: 160, height: 160)
              .padding(10).background(.white)
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
    return NSImage(cgImage: cg, size: img.extent.size)
  }
}
