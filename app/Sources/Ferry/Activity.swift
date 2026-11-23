import FerryCore
import SwiftUI

struct Activity: View {
  let store: Store
  @State private var selected = ""
  @State private var dismissed = Set<String>()
  @State private var receipt: JSON?
  var body: some View {
    let waiting = store.approvals.keys.sorted().filter { !dismissed.contains($0) }
    ForEach(waiting, id: \.self) { id in
      if let request = store.approvals[id] {
        Panel(title: "Agent approvals", glow: Palette.warn) {
          Label(
            "Triggered by " + request["actor"].text.replacingOccurrences(of: "agent:", with: ""),
            systemImage: "hand.raised"
          )
          .font(.inter(20, .semibold))
          ForEach(request["reasons"].rows, id: \.self) { Text($0.text) }
          HStack {
            PillButton(title: "Inspect plan", role: id == waiting.first ? .primary : .secondary) {
              store.perform {
                store.review = try await store.rpc("plan.get", ["plan_id": .string(id)])
                store.section = "Transfer"
              }
            }
            PillButton(title: "Dismiss") { dismissed.insert(id) }
          }
        }
      }
    }
    HStack(alignment: .top, spacing: 12) {
      VStack(spacing: 12) {
        Panel(title: "Transfer queue") {
          if store.transfers.isEmpty { Text("No transfers yet").foregroundStyle(Palette.ink4) }
          ForEach(store.transfers, id: \.self) { row in
            Button {
              selected = row["id"].text
            } label: {
              HStack {
                Text(row["id"].text).font(.system(.body, design: .monospaced))
                Spacer()
                StateLabel(word: row["status"].text)
              }.contentShape(Rectangle())
            }.buttonStyle(.plain)
          }
        }
        Panel(title: "Receipt history") {
          if store.receipts.isEmpty {
            Text("Receipts appear when a transfer finishes.").foregroundStyle(Palette.ink4)
          }
          ForEach(store.receipts, id: \.self) { row in
            Button {
              receipt = row
            } label: {
              HStack {
                Text(row["receipt_id"].text)
                Spacer()
                StateLabel(word: row["status"].text)
              }
            }.buttonStyle(.plain)
          }
        }
      }
      if let row = store.transfers.first(where: { $0["id"].text == selected })
        ?? store.transfers.first
      {
        TransferInspector(store: store, row: row).id(row["id"])
      }
    }
    .sheet(isPresented: Binding(get: { receipt != nil }, set: { if !$0 { receipt = nil } })) {
      if let receipt { ReceiptView(store: store, row: receipt) }
    }
  }
}
struct TransferInspector: View {
  let store: Store
  let row: JSON
  @State private var detail: JSON = .null
  var body: some View {
    let live = store.progress[row["id"].text] ?? detail["progress"]
    Panel(title: "Transfer inspector") {
      if live != .null {
        HStack {
          Text("\(Int((live["overall_fraction"].double * 100).rounded()))%").font(
            .inter(22, .semibold))
          Spacer()
          Text(
            "ETA  "
              + Format.eta(
                live["eta_seconds"] == .null ? -1 : Int64(ceil(live["eta_seconds"].double))))
        }
        ForEach(live["targets"].rows, id: \.self) { target in
          Text(target["label"].text + " · " + target["transport_badge"].text).font(
            .inter(15, .semibold))
          ForEach(["Write", "Physical readback verify"], id: \.self) { title in
            let write = title == "Write"
            let bytes = target[write ? "bytes_written" : "bytes_verified"].count
            HStack {
              Text(title)
              Spacer()
              Text(
                Format.bytes(bytes) + " · "
                  + Format.bytes(Int64(target[write ? "write_bps" : "verify_bps"].double)) + "/s")
            }
            GeometryReader { proxy in
              let fraction = min(1, Double(bytes) / max(1, target["total_bytes"].double))
              Capsule().fill(Palette.rule3).overlay(alignment: .leading) {
                Capsule().fill(Palette.run).frame(width: proxy.size.width * fraction)
              }
            }.frame(height: 5)
          }
          Detail(text: target["current_file"].text)
        }
      } else {
        StateLabel(word: row["status"].text)
        ForEach(detail["targets"].rows, id: \.self) { t in
          Text(t["label"].text + " · " + t["status"].text)
        }
      }
      ForEach(detail["failures"].rows, id: \.self) { f in
        Detail(text: f["dest_rel"].text + " · " + f["error"].text)
      }
      if row["status"] == "running" {
        PillButton(title: "Cancel transfer", role: .destructive) { action("transfer.cancel") }
      }
      if ["needs_attention", "partial", "canceled", "failed"].contains(row["status"].text) {
        PillButton(title: "Resume") { action("transfer.resume") }
      }
    }.task(id: row["status"]) {
      do { detail = try await store.rpc("transfer.get", ["transfer_id": row["id"]]) } catch {
        store.error = error.localizedDescription
      }
    }
  }
  func action(_ method: String) {
    store.perform {
      _ = try await store.rpc(method, ["transfer_id": row["id"]])
      try await store.refresh()
    }
  }
}
struct ReceiptView: View {
  let store: Store
  let row: JSON
  @State private var doc: JSON = .null
  @State private var verification: JSON = .null
  @State private var path = ""
  @State private var failure = ""
  @State private var busy = false
  var ref: JSON { ["receipt_id": row["receipt_id"]] }
  var body: some View {
    SheetPage(title: "Transfer receipt") {
      StateLabel(word: row["status"].text)
      Detail(text: row["receipt_id"].text)
      Text(doc["actor"].text + " · " + doc["checksum_algo"].text.uppercased())
      Detail(text: doc["receipt_digest"].text)
      Button("Copy digest") { copyText(doc["receipt_digest"].text) }
      Detail(text: path)
      ForEach(doc["targets"].rows, id: \.self) {
        Text(
          [$0["label"].text, $0["status"].text, $0["readback_mode"].text].joined(separator: " · "))
      }
      if !failure.isEmpty {
        Text(failure)
        Button("Retry export") {
          store.perform {
            path = try await store.rpc("receipt.export", ref)["path"].text
            failure = ""
          }
        }
      }
      PillButton(
        title: busy ? "Verifying…" : "Verify on Disk", role: .primary,
        enabled: !busy && doc != .null
      ) {
        busy = true
        store.perform {
          defer { busy = false }
          verification = try await store.rpc("verify.run", ref)
        }
      }
      if verification != .null {
        StateLabel(word: verification["clean"] == .bool(true) ? "clean" : "needs_review")
        Text(
          ["checked", "missing", "modified", "added", "unreadable"].map {
            "\(verification[$0].count) \($0)"
          }.joined(separator: " · "))
        ForEach(verification["targets"].rows, id: \.self) { t in
          Text(t["label"].text).bold()
          Text("\(t["checked"].count) checked")
          ForEach(["missing", "modified", "added", "unreadable"], id: \.self) { kind in
            ForEach(t[kind].rows, id: \.self) { item in
              Detail(
                text: kind.capitalized + " · "
                  + (kind == "unreadable"
                    ? item["path"].text + " · " + item["error"].text : item.text))
            }
          }
        }
      }
      DisclosureGroup("Files (\(doc["files"].rows.count))") {
        LazyVStack(alignment: .leading, spacing: 8) {
          ForEach(doc["files"].rows, id: \.self) { file in
            Detail(text: file["dest_rel_path"].text)
            Text(
              [file["action"].text, file["readback_mode"].text, Format.bytes(file["size"].count)]
                .joined(separator: " · "))
          }
        }
      }
      DisclosureGroup("Complete receipt JSON") { Detail(text: doc.pretty) }
    }.task {
      path = row["exported_path"].text
      failure = row["export_error"].text
      do { doc = try await store.rpc("receipt.get", ref) } catch {
        failure = error.localizedDescription
      }
    }
  }
}
