import FerryCore
import SwiftUI

/// Every transfer and every request, the way Park's Inbox reads: the list at the left on the
/// open room, the one you are looking at raised on glass beside it.
struct Activity: View {
  @Bindable var store: Store
  @Bindable var chrome: Chrome
  @State private var selected = ""
  @State private var dismissed = Set<String>()
  @State private var search = ""
  @FocusState private var searching: Bool

  static let filters = ["All", "Active", "Approvals", "Attention", "Failed", "Done"]

  var body: some View {
    let records = matchingRecords
    let current = records.first(where: { $0.id == selected }) ?? records.first
    PageFrame(scrolls: false) {
      PageHead(
        title: "Activity",
        note: store.approvals.isEmpty
          ? nil : "\(store.approvals.count) \(store.approvals.count == 1 ? "needs" : "need") you")
    } content: { width in
      VStack(alignment: .leading, spacing: 0) {
        let wide = width >= 940
        HStack(alignment: .bottom, spacing: Space.s5) {
          UnderlineTabs(
            choices: Self.filters, selection: $chrome.activityFilter,
            counts: [
              "Approvals": store.approvals.count,
              "Active": store.progress.count,
            ])
          Spacer(minLength: Space.s3)
          if wide { searchField.frame(width: 230).padding(.bottom, 8) }
        }
        Hairline(color: Palette.rule2)
        HStack(alignment: .top, spacing: 0) {
          VStack(spacing: 0) {
            if !wide { searchField.padding(.top, Space.s4) }
            GlassScroll {
              list(records, current: current).padding(.vertical, Space.s4).padding(.trailing, 4)
            }
          }
          .frame(width: wide ? min(380, width * 0.36) : 300)
          Rectangle().fill(Palette.rule1).frame(width: 1).padding(.horizontal, Space.s6)
            .padding(.vertical, Space.s4)
          GlassScroll {
            inspector(current, records: records).padding(.vertical, Space.s4)
              .padding(.bottom, Space.s7)
          }
        }
      }
    }
    .onChange(of: chrome.searchRequest, initial: true) { old, new in
      if new != old { searching = true }
    }
  }

  private var searchField: some View {
    TextField("Search activity", text: $search)
      .textFieldStyle(ParkFieldStyle(icon: "magnifyingglass"))
      .focused($searching)
  }

  @ViewBuilder private func list(_ records: [Record], current: Record?) -> some View {
    VStack(spacing: 0) {
      ForEach(Array(records.enumerated()), id: \.element.id) { index, item in
        let on = current?.id == item.id
        let neighbour = index > 0 && (records[index - 1].id == current?.id)
        if index > 0 { Hairline(color: on || neighbour ? .clear : Palette.rule1) }
        RecordRow(item: item, on: on) { selected = item.id }
      }
      if records.isEmpty { Quiet("No matching activity.").frame(maxWidth: .infinity, alignment: .leading) }
    }
  }

  @ViewBuilder private func inspector(_ current: Record?, records: [Record]) -> some View {
    if let item = current {
      VStack(alignment: .leading, spacing: Space.s5) {
        if item.approval {
          ApprovalCard(store: store, id: item.id, row: item.row)
        } else {
          if store.transfers.contains(where: { $0["id"].text == item.id }) {
            TransferInspector(store: store, row: item.row).id(item.id)
          }
          if let receipt = store.receipts.first(where: { $0["transfer_id"].text == item.id }) {
            ReceiptView(store: store, row: receipt).id(receipt["receipt_id"])
          }
        }
        if !item.approval, chrome.activityFilter == "All",
          let next = records.first(where: { $0.approval && !dismissed.contains($0.id) })
        {
          VStack(alignment: .leading, spacing: Space.s3) {
            SectionTitle("Also waiting on you")
            ApprovalCard(store: store, id: next.id, row: next.row, compact: true) {
              dismissed.insert(next.id)
            }
          }
          .padding(.top, Space.s3)
        }
      }
    } else {
      Quiet("Choose a transfer to inspect it.")
    }
  }

  struct Record: Identifiable {
    let id: String
    let row: JSON
    let approval: Bool
    var actor: String { row["actor"].text.replacingOccurrences(of: "agent:", with: "") }
    var status: String { approval ? "needs approval" : row["status"].text }
    var when: String { row["started_at"].text.isEmpty ? row["created_at"].text : row["started_at"].text }
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
    let filter = chrome.activityFilter
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

/// One line of the log; the one being inspected is raised on glass, as Park's Inbox raises
/// the item it shows.
private struct RecordRow: View {
  let item: Activity.Record
  let on: Bool
  let action: () -> Void
  @State private var hovering = false

  private var words: some View {
    VStack(alignment: .leading, spacing: 4) {
      Text(item.approval ? "Request from \(item.actor)" : item.id)
        .font(.inter(TypeScale.base, .semibold)).foregroundStyle(on ? Palette.bone : Palette.ink1)
        .lineLimit(1).truncationMode(.middle)
      Text(
        item.approval
          ? item.id : [item.actor, Stamp.day(item.when)].filter { !$0.isEmpty }.joined(separator: " · ")
      )
      .font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3).lineLimit(1)
    }
  }

  var body: some View {
    Button(action: action) {
      ViewThatFits(in: .horizontal) {
        HStack(spacing: Space.s3) {
          words.fixedSize()
          Spacer(minLength: Space.s2)
          StatusPill(word: item.status)
        }
        VStack(alignment: .leading, spacing: Space.s2) {
          words
          StatusPill(word: item.status)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
      }
      .padding(.horizontal, Space.s4).padding(.vertical, 14)
      .background {
        if on {
          GlassSurface(radius: 16)
        } else if hovering {
          RoundedRectangle(cornerRadius: 16, style: .continuous).fill(Color.white.opacity(0.04))
        }
      }
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
    .onHover { hovering = $0 }
    .accessibilityAddTraits(on ? .isSelected : [])
  }
}

/// A transfer, raised: how far each target has come, written and read back.
struct TransferInspector: View {
  let store: Store
  let row: JSON
  @State private var detail: JSON = .null

  var body: some View {
    let live = store.progress[row["id"].text] ?? detail["progress"]
    VStack(alignment: .leading, spacing: Space.s5) {
      HStack(alignment: .center, spacing: Space.s3) {
        VStack(alignment: .leading, spacing: 4) {
          FieldLabel("Transfer")
          Text(row["id"].text).font(.inter(TypeScale.lg, .semibold)).foregroundStyle(Palette.bone)
            .textSelection(.enabled)
        }
        Spacer()
        StatusPill(word: row["status"].text)
      }
      if live != .null {
        HStack(alignment: .firstTextBaseline, spacing: Space.s4) {
          HStack(alignment: .firstTextBaseline, spacing: 2) {
            Text("\(Int((live["overall_fraction"].double * 100).rounded()))").font(.inter(40, .semibold))
              .tracking(-1)
            Text("%").font(.inter(TypeScale.md, .medium)).foregroundStyle(Palette.ink3)
          }
          .foregroundStyle(Palette.bone)
          Text(
            Format.eta(live["eta_seconds"] == .null ? -1 : Int64(ceil(live["eta_seconds"].double)))
              + " left"
          )
          .font(.inter(TypeScale.base)).foregroundStyle(Palette.ink2)
        }
        ForEach(live["targets"].rows, id: \.self) { target in
          VStack(alignment: .leading, spacing: Space.s3) {
            HStack(spacing: Space.s2) {
              Text(target["label"].text).font(.inter(TypeScale.base, .semibold))
              TransportBadge(text: target["transport_badge"].text)
            }
            ForEach(["Written", "Read back & verified"], id: \.self) { title in
              let write = title == "Written"
              let bytes = target[write ? "bytes_written" : "bytes_verified"].count
              VStack(alignment: .leading, spacing: 6) {
                HStack {
                  Text(title).foregroundStyle(Palette.ink2)
                  Spacer()
                  Text(
                    Format.bytes(bytes) + " · "
                      + Format.bytes(Int64(target[write ? "write_bps" : "verify_bps"].double)) + "/s"
                  )
                  .foregroundStyle(Palette.ink3)
                }
                .font(.inter(TypeScale.xs, .medium))
                Meter(
                  fraction: Double(bytes) / max(1, target["total_bytes"].double),
                  color: write ? Palette.run : Palette.ok)
              }
            }
            HStack(spacing: Space.s2) {
              Image(systemName: "film").font(.system(size: 11)).foregroundStyle(Palette.ink4)
              Detail(text: target["current_file"].text)
            }
          }
        }
      } else {
        ForEach(detail["targets"].rows, id: \.self) { t in
          HStack {
            Text(t["label"].text).font(.inter(TypeScale.sm, .medium))
            Spacer()
            StatusPill(word: t["status"].text)
          }
        }
      }
      if !detail["failures"].rows.isEmpty {
        VStack(alignment: .leading, spacing: 6) {
          FieldLabel("Failures")
          ForEach(detail["failures"].rows, id: \.self) { f in
            Detail(text: f["dest_rel"].text + " · " + f["error"].text, color: Palette.bad)
          }
        }
      }
      if row["status"] == "running"
        || ["needs_attention", "partial", "canceled", "failed"].contains(row["status"].text)
      {
        HStack(spacing: Space.s3) {
          if ["needs_attention", "partial", "canceled", "failed"].contains(row["status"].text) {
            Button("Resume") { action("transfer.resume") }.buttonStyle(.primary)
          }
          if row["status"] == "running" {
            Button("Cancel transfer") { action("transfer.cancel") }.buttonStyle(.destructive)
          }
        }
      }
    }
    .padding(Space.s6)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(GlassSurface(radius: 20))
    .task(id: row["status"]) {
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

/// A receipt, raised: what was written, its digest, and the way to check it on disk again.
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
    VStack(alignment: .leading, spacing: Space.s4) {
      HStack(alignment: .center, spacing: Space.s3) {
        VStack(alignment: .leading, spacing: 4) {
          FieldLabel(doc["schema"].text.isEmpty ? "Receipt" : "Receipt · " + doc["schema"].text)
          Text(row["receipt_id"].text).font(.inter(TypeScale.lg, .semibold)).foregroundStyle(Palette.bone)
            .textSelection(.enabled)
        }
        Spacer()
        StatusPill(word: row["status"].text)
      }
      VStack(spacing: 7) {
        fact("Actor", doc["actor"].text)
        fact("Checksum", doc["checksum_algo"].text.uppercased())
        HStack(alignment: .firstTextBaseline) {
          Text("Digest").font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
          Spacer(minLength: Space.s3)
          Detail(text: doc["receipt_digest"].text, color: Palette.ink1)
          Button {
            copyText(doc["receipt_digest"].text)
          } label: {
            Image(systemName: "doc.on.doc").font(.system(size: 10, weight: .medium))
          }
          .buttonStyle(.park(.quiet, .small, disc: true))
          .help("Copy digest")
        }
        if !path.isEmpty { fact("Exported to", path, mono: true) }
      }
      if !doc["targets"].rows.isEmpty {
        VStack(spacing: 0) {
          ForEach(doc["targets"].rows, id: \.self) { target in
            Hairline()
            HStack {
              Text(target["label"].text).font(.inter(TypeScale.sm, .medium))
              Text(display(target["readback_mode"].text) + " readback").font(.inter(TypeScale.xs))
                .foregroundStyle(Palette.ink3)
              Spacer()
              StatusPill(word: target["status"].text)
            }
            .padding(.vertical, 9)
          }
          Hairline()
        }
      }
      if !failure.isEmpty {
        HStack {
          Text(failure).font(.inter(TypeScale.xs)).foregroundStyle(Palette.warn)
          Spacer()
          Button("Retry export") {
            store.perform {
              path = try await store.rpc("receipt.export", ref)["path"].text
              failure = ""
            }
          }
          .buttonStyle(.park(.standard, .small))
        }
      }
      HStack(spacing: Space.s3) {
        Button {
          busy = true
          store.perform {
            defer { busy = false }
            verification = try await store.rpc("verify.run", ref)
          }
        } label: {
          Label(busy ? "Verifying…" : "Verify on disk", systemImage: "checkmark.shield")
        }
        .buttonStyle(.primary)
        .disabled(busy || doc == .null)
        if verification != .null {
          StatusPill(word: verification["clean"] == .bool(true) ? "clean" : "needs_review")
        }
      }
      if verification != .null {
        Text(
          ["checked", "missing", "modified", "added", "unreadable"].map {
            "\(verification[$0].count) \($0)"
          }.joined(separator: " · ")
        )
        .font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink2)
        ForEach(verification["targets"].rows, id: \.self) { t in
          VStack(alignment: .leading, spacing: 4) {
            Text("\(t["label"].text) · \(t["checked"].count) checked").font(.inter(TypeScale.xs, .semibold))
            ForEach(["missing", "modified", "added", "unreadable"], id: \.self) { kind in
              ForEach(t[kind].rows, id: \.self) { item in
                Detail(
                  text: kind.capitalized + " · "
                    + (kind == "unreadable" ? item["path"].text + " · " + item["error"].text : item.text),
                  color: Palette.bad)
              }
            }
          }
        }
      }
      Fold(title: "Files (\(doc["files"].rows.count))") {
        LazyVStack(alignment: .leading, spacing: 0) {
          ForEach(doc["files"].rows, id: \.self) { file in
            VStack(alignment: .leading, spacing: 3) {
              Detail(text: file["dest_rel_path"].text, color: Palette.ink1)
              Text(
                [file["action"].text, file["readback_mode"].text, Format.bytes(file["size"].count)]
                  .joined(separator: " · ")
              )
              .font(.inter(11)).foregroundStyle(Palette.ink4)
            }
            .padding(.vertical, 6)
          }
        }
      }
      Fold(title: "Complete receipt JSON") {
        Text(doc.pretty).font(.mono(11)).foregroundStyle(Palette.ink3).textSelection(.enabled)
          .frame(maxWidth: .infinity, alignment: .leading)
          .padding(Space.s3)
          .parkField(radius: 12)
      }
    }
    .padding(Space.s6)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(GlassSurface(radius: 20))
    .task {
      path = row["exported_path"].text
      failure = row["export_error"].text
      do { doc = try await store.rpc("receipt.get", ref) } catch {
        failure = error.localizedDescription
      }
    }
  }

  private func fact(_ name: String, _ value: String, mono: Bool = false) -> some View {
    HStack(alignment: .firstTextBaseline) {
      Text(name).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
      Spacer(minLength: Space.s3)
      if mono {
        Detail(text: value, color: Palette.ink1)
      } else {
        Text(value.isEmpty ? "—" : value).font(.inter(TypeScale.xs, .medium)).foregroundStyle(Palette.ink1)
      }
    }
  }
}
