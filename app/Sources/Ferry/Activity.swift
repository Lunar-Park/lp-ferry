import FerryCore
import SwiftUI

struct Activity: View {
  let store: Store
  @State private var selected = ""
  @State private var filter = "All"
  @State private var dismissed = Set<String>()
  @State private var search = ""
  var body: some View {
    let records = matchingRecords
    let current = records.first(where: { $0.id == selected }) ?? records.first
    HStack {
      Segments(
        choices: ["All", "Active", "Approvals", "Attention", "Failed", "Done"], selection: $filter)
      Spacer()
      TextField("Search activity", text: $search).frame(maxWidth: 210)
    }
    HStack(alignment: .top, spacing: 12) {
      Panel(title: "Transfer log") {
        HStack {
          ColumnLabel("Transfer / plan")
          Spacer()
          ColumnLabel("State")
        }
        ForEach(records) { item in
          Divider()
          Button {
            selected = item.id
          } label: {
            HStack(spacing: 8) {
              Capsule().fill(current?.id == item.id ? Palette.accent : .clear).frame(
                width: 2, height: 25)
              VStack(alignment: .leading, spacing: 4) {
                Text(item.id).font(.inter(13, .semibold)).lineLimit(1).truncationMode(.middle)
                Text(item.actor).font(.inter(11)).foregroundStyle(Palette.ink3)
              }
              Spacer()
              StateLabel(word: item.status)
            }.contentShape(Rectangle())
          }
        }
        if records.isEmpty { Text("No matching activity").foregroundStyle(Palette.ink3) }
      }
      if let item = current {
        VStack(alignment: .leading, spacing: 12) {
          if item.approval {
            approvalCard(item)
          } else {
            if store.transfers.contains(where: { $0["id"].text == item.id }) {
              TransferInspector(store: store, row: item.row).id(item.id)
            }
            if let receipt = store.receipts.first(where: { $0["transfer_id"].text == item.id }) {
              Panel(title: "Receipt inspector") { ReceiptView(store: store, row: receipt) }.id(
                receipt["receipt_id"])
            }
          }
          if !item.approval, filter == "All",
            let next = records.first(where: { $0.approval && !dismissed.contains($0.id) })
          {
            approvalCard(next)
          }
        }.frame(maxWidth: .infinity)
      }
    }
  }
  func approvalCard(_ item: Record) -> some View {
    Panel(title: "Approval review", glow: Palette.warn) {

      Text("Triggered by " + item.actor).font(.inter(18, .semibold))
      ForEach(item.row["reasons"].rows, id: \.self) { Text($0.text) }
      HStack {
        PillButton(title: "Review plan", role: .primary) {
          store.perform {
            store.review = try await store.rpc("plan.get", ["plan_id": .string(item.id)])
            store.section = "Transfer"
          }
        }
        if filter != "Approvals" {
          Button("Dismiss") { dismissed.insert(item.id) }.foregroundStyle(Palette.ink3)
        }
      }
    }
  }
  struct Record: Identifiable {
    let id: String
    let row: JSON
    let approval: Bool
    var actor: String { row["actor"].text.replacingOccurrences(of: "agent:", with: "") }
    var status: String { approval ? "needs approval" : row["status"].text }
  }
  var matchingRecords: [Record] {
    var records = store.transfers.map { Record(id: $0["id"].text, row: $0, approval: false) }
    let ids = Set(records.map(\.id))
    records += store.receipts.filter { !ids.contains($0["transfer_id"].text) }.map {
      Record(id: $0["transfer_id"].text, row: $0, approval: false)
    }
    records += store.approvals.keys.sorted().compactMap { id in
      store.approvals[id].map { Record(id: id, row: $0, approval: true) }
    }
    return records.filter { item in
      let matches: Bool =
        switch filter {
        case "Active": ["running", "verifying", "queued"].contains(item.status)
        case "Approvals": item.approval
        case "Attention": ["partial", "needs_attention"].contains(item.status)
        case "Failed": item.status == "failed"
        case "Done": ["completed", "canceled"].contains(item.status)
        default: true
        }
      if item.approval && dismissed.contains(item.id) && filter != "Approvals" { return false }
      return matches
        && (search.isEmpty
          || [item.id, item.status, item.actor].joined(separator: " ")
            .localizedCaseInsensitiveContains(search))
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
          TransportBadge(text: target["transport_badge"].text)
          Text(target["label"].text).font(
            .inter(15, .semibold))
          ForEach(["Write", "Physical readback verify"], id: \.self) { title in
            let write = title == "Write"
            let bytes = target[write ? "bytes_written" : "bytes_verified"].count
            VStack(alignment: .leading, spacing: 3) {
              Text(title)
              Text(
                Format.bytes(bytes) + " · "
                  + Format.bytes(Int64(target[write ? "write_bps" : "verify_bps"].double)) + "/s")
            }
            .font(.inter(12))
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
    VStack(alignment: .leading, spacing: 10) {
      ColumnLabel(doc["schema"].text)
      StateLabel(word: row["status"].text)
      Detail(text: row["receipt_id"].text)
      Text(doc["actor"].text + " · " + doc["checksum_algo"].text.uppercased())
      Detail(text: doc["receipt_digest"].text)
      Button("Copy digest") { copyText(doc["receipt_digest"].text) }
      if !path.isEmpty { Detail(text: path) }
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
