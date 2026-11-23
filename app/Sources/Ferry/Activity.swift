import FerryCore
import SwiftUI

/// Every copy and every request: the list at the left on the open room, the one you are
/// looking at raised on glass beside it. Titles say what went where; ids stay in the detail.
struct Activity: View {
  @Bindable var store: Store
  @Bindable var chrome: Chrome
  @State private var search = ""

  static let filters = ["All", "Active", "Approvals", "Completed"]

  var body: some View {
    let records = matchingRecords
    let current = records.first(where: { $0.id == chrome.activitySelection }) ?? records.first
    PageFrame(scrolls: false) {
      PageHead(
        title: "Activity",
        note: store.approvals.isEmpty
          ? nil : "\(store.approvals.count) waiting for you")
    } content: { width in
      VStack(alignment: .leading, spacing: 0) {
        HStack(alignment: .bottom, spacing: Space.s5) {
          UnderlineTabs(
            choices: Self.filters, selection: $chrome.activityFilter,
            counts: ["Approvals": store.approvals.count, "Active": store.progress.count])
            .padding(.top, Space.s3)
          Spacer(minLength: Space.s3)
          if allRecords.count > 8 {
            TextField("Search", text: $search)
              .textFieldStyle(ParkFieldStyle(icon: "magnifyingglass"))
              .frame(width: 200).padding(.bottom, 8)
          }
        }
        Hairline(color: Palette.rule2)
        HStack(alignment: .top, spacing: 0) {
          GlassScroll {
            list(records, current: current).padding(.vertical, Space.s4).padding(.trailing, 4)
          }
          .frame(width: width >= 940 ? 340 : 280)
          Rectangle().fill(Palette.rule1).frame(width: 1).padding(.horizontal, Space.s6)
            .padding(.vertical, Space.s4)
          GlassScroll {
            Group {
              if let item = current {
                if item.approval {
                  RequestDetail(store: store, chrome: chrome, id: item.id, row: item.row)
                } else {
                  TransferDetail(store: store, chrome: chrome, record: item).id(item.id)
                }
              } else {
                Quiet("Nothing here yet. Copies you start show up here, with their receipts.")
              }
            }
            .padding(.vertical, Space.s4).padding(.bottom, Space.s7)
          }
        }
      }
    }
    .task(id: allRecords.map(\.planID)) {
      await chrome.routes.load(allRecords.map(\.planID), store: store)
    }
  }

  @ViewBuilder private func list(_ records: [Record], current: Record?) -> some View {
    VStack(spacing: 2) {
      ForEach(records) { item in
        RecordRow(item: item, title: title(item), detail: detail(item), on: current?.id == item.id) {
          chrome.activitySelection = item.id
        }
      }
      if records.isEmpty {
        Quiet(chrome.activityFilter == "All" ? "No copies yet." : "Nothing \(chrome.activityFilter.lowercased()).")
          .frame(maxWidth: .infinity, alignment: .leading)
      }
    }
  }

  private func title(_ item: Record) -> String {
    item.approval
      ? chrome.routes.title(request: item.row)
      : chrome.routes.title(transfer: item.row, live: store.progress[item.id])
  }

  private func detail(_ item: Record) -> String {
    if item.approval { return "Waiting for your approval" }
    if let live = store.progress[item.id] {
      let eta = live["eta_seconds"] == .null ? -1 : Int64(ceil(live["eta_seconds"].double))
      return "\(Int((live["overall_fraction"].double * 100).rounded()))% · \(Format.eta(eta)) left"
    }
    let when = Stamp.day(item.when)
    let who = Routes.actor(item.row)
    return [when, who == "You" ? "" : "by \(who)"].filter { !$0.isEmpty }.joined(separator: " · ")
  }

  struct Record: Identifiable {
    let id: String
    let row: JSON
    let approval: Bool
    var planID: String { approval ? id : row["plan_id"].text }
    var status: String { approval ? "needs approval" : row["status"].text }
    var when: String {
      for key in ["ended_at", "started_at", "created_at"] where !row[key].text.isEmpty {
        return row[key].text
      }
      return ""
    }
    var active: Bool { ["running", "verifying", "queued"].contains(status) }
  }

  private var allRecords: [Record] {
    var records = store.approvals.keys.sorted().compactMap { id in
      store.approvals[id].map { Record(id: id, row: $0, approval: true) }
    }
    records += store.transfers.map { Record(id: $0["id"].text, row: $0, approval: false) }
    let ids = Set(records.map(\.id))
    records += store.receipts.filter { !ids.contains($0["transfer_id"].text) }.map {
      Record(id: $0["transfer_id"].text, row: $0, approval: false)
    }
    return records
  }

  var matchingRecords: [Record] {
    allRecords.filter { item in
      let matches: Bool =
        switch chrome.activityFilter {
        case "Active": item.active
        case "Approvals": item.approval
        case "Completed": !item.approval && !item.active
        default: true
        }
      return matches
        && (search.isEmpty
          || [title(item), item.id, item.status].joined(separator: " ")
            .localizedCaseInsensitiveContains(search))
    }
  }
}

/// One line of the log: what went where, when, and a quiet status mark. The one being looked
/// at sits on a soft plate.
private struct RecordRow: View {
  let item: Activity.Record
  let title: String
  let detail: String
  let on: Bool
  let action: () -> Void
  @State private var hovering = false

  var body: some View {
    Button(action: action) {
      HStack(spacing: Space.s3) {
        StatusDot(status: item.status)
        VStack(alignment: .leading, spacing: 4) {
          Text(title).font(.inter(TypeScale.base, .semibold))
            .foregroundStyle(on ? Palette.bone : Palette.ink1).lineLimit(1)
          Text(detail).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3).lineLimit(1)
        }
        Spacer(minLength: 0)
      }
      .padding(.horizontal, Space.s3).padding(.vertical, 13)
      .background(
        RoundedRectangle(cornerRadius: 14, style: .continuous)
          .fill(Color.white.opacity(on ? 0.075 : hovering ? 0.035 : 0))
      )
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
    .onHover { hovering = $0 }
    .help(StatusPill.words(item.status))
    .accessibilityLabel("\(title), \(StatusPill.words(item.status)), \(detail)")
    .accessibilityAddTraits(on ? .isSelected : [])
  }
}

/// A status as a small shape: a ring that turns for running, a tick, a hand, a cross.
struct StatusDot: View {
  let status: String

  var body: some View {
    let (color, symbol) = StatusPill.style(status)
    Image(systemName: symbol).font(.system(size: 9, weight: .bold)).foregroundStyle(color)
      .frame(width: 24, height: 24)
      .background(Circle().fill(color.opacity(0.13)))
  }
}

/// A copy, raised: how far it has come, written and checked, and its receipt.
struct TransferDetail: View {
  let store: Store
  let chrome: Chrome
  let record: Activity.Record
  @State private var detail: JSON = .null

  private var row: JSON { record.row }
  private var resumable: Bool {
    ["needs_attention", "partial", "canceled", "failed"].contains(row["status"].text)
  }

  var body: some View {
    let live = store.progress[record.id] ?? detail["progress"]
    let receipt = store.receipts.first { $0["transfer_id"].text == record.id }
    VStack(alignment: .leading, spacing: Space.s5) {
      VStack(alignment: .leading, spacing: Space.s2) {
        StatusPill(word: row["status"].text)
        Text(chrome.routes.title(transfer: row, live: store.progress[record.id]))
          .font(.inter(TypeScale.lg, .semibold)).foregroundStyle(Palette.bone)
          .fixedSize(horizontal: false, vertical: true)
        Text(meta).font(.mono(11)).foregroundStyle(Palette.ink4).textSelection(.enabled).lineLimit(1)
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
            Format.eta(live["eta_seconds"] == .null ? -1 : Int64(ceil(live["eta_seconds"].double))) + " left"
          )
          .font(.inter(TypeScale.base)).foregroundStyle(Palette.ink2)
          Spacer(minLength: 0)
          if row["status"] == "running" {
            Button("Cancel") { action("transfer.cancel") }.buttonStyle(.park(.destructive, .small))
          }
        }
        ForEach(live["targets"].rows, id: \.self) { target in
          VStack(alignment: .leading, spacing: Space.s3) {
            if live["targets"].rows.count > 1 {
              HStack(spacing: Space.s3) {
                Text(target["label"].text).font(.inter(TypeScale.sm, .semibold))
                TransportBadge(text: target["transport_badge"].text)
              }
            }
            bar(
              "Written", target["bytes_written"].count, rate: target["write_bps"].double,
              total: target["total_bytes"].double, color: Palette.run)
            bar(
              "Verified on disk", target["bytes_verified"].count, rate: target["verify_bps"].double,
              total: target["total_bytes"].double, color: Palette.ok)
            if !target["current_file"].text.isEmpty {
              HStack(spacing: Space.s2) {
                Image(systemName: "film").font(.system(size: 11)).foregroundStyle(Palette.ink4)
                Detail(text: target["current_file"].text)
              }
            }
          }
        }
      } else if receipt == nil && !detail["targets"].rows.isEmpty {
        VStack(spacing: 0) {
          ForEach(detail["targets"].rows, id: \.self) { t in
            HStack {
              Text(t["label"].text).font(.inter(TypeScale.sm, .medium))
              Spacer()
              if t["bytes_verified"].count > 0 {
                Text(Format.bytes(t["bytes_verified"].count) + " verified").font(.inter(TypeScale.xs))
                  .foregroundStyle(Palette.ink3)
              }
            }
            .padding(.vertical, 6)
          }
        }
      }
      if !detail["failures"].rows.isEmpty {
        VStack(alignment: .leading, spacing: 6) {
          Text("\(detail["failures"].rows.count) files did not land").font(.inter(TypeScale.sm, .semibold))
            .foregroundStyle(Palette.bad)
          ForEach(detail["failures"].rows, id: \.self) { f in
            Detail(text: f["dest_rel"].text + " · " + f["error"].text, color: Palette.ink3)
          }
        }
      }
      if resumable {
        HStack(spacing: Space.s3) {
          Button {
            action("transfer.resume")
          } label: {
            Label("Resume", systemImage: "arrow.clockwise")
          }
          .buttonStyle(.primary)
          Text("Only the files that did not land are copied again.").font(.inter(TypeScale.xs))
            .foregroundStyle(Palette.ink3)
        }
      }
      if let receipt {
        Hairline()
        ReceiptView(store: store, row: receipt, primary: !resumable).id(receipt["receipt_id"])
      }
    }
    .padding(Space.s6)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(GlassSurface(radius: 22))
    .task(id: row["status"]) {
      do { detail = try await store.rpc("transfer.get", ["transfer_id": .string(record.id)]) } catch {
        if row["status"] == "running" { store.error = error.localizedDescription }
      }
    }
  }

  private var meta: String {
    let who = Routes.actor(row)
    return [
      record.id, Stamp.day(row["started_at"].text).nilIfEmpty.map { "started \($0)" } ?? "",
      who == "You" ? "" : "by \(who)",
    ].filter { !$0.isEmpty }.joined(separator: " · ")
  }

  private func bar(_ title: String, _ bytes: Int64, rate: Double, total: Double, color: Color) -> some View {
    VStack(alignment: .leading, spacing: 6) {
      HStack {
        Text(title).foregroundStyle(Palette.ink2)
        Spacer()
        Text("\(Format.bytes(bytes)) of \(Format.bytes(Int64(total))) · \(Format.bytes(Int64(rate)))/s")
          .foregroundStyle(Palette.ink3)
      }
      .font(.inter(TypeScale.xs, .medium))
      Meter(fraction: Double(bytes) / max(1, total), color: color)
    }
  }

  func action(_ method: String) {
    store.perform {
      _ = try await store.rpc(method, ["transfer_id": .string(record.id)])
      try await store.refresh()
    }
  }
}

/// An agent's request, raised: what it wants, why it waits, and the way to review it.
struct RequestDetail: View {
  let store: Store
  let chrome: Chrome
  let id: String
  let row: JSON

  var body: some View {
    VStack(alignment: .leading, spacing: Space.s4) {
      HStack(spacing: 6) {
        Image(systemName: "hand.raised").font(.system(size: 11, weight: .semibold))
        Text("Waiting for your approval").font(.inter(13, .semibold))
      }
      .foregroundStyle(Palette.warn)
      Text(chrome.routes.sentence(request: row)).font(.inter(TypeScale.lg, .semibold))
        .foregroundStyle(Palette.bone).fixedSize(horizontal: false, vertical: true)
      ForEach(row["reasons"].rows, id: \.self) { reason in
        Text(reason.text).font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink2)
      }
      if row["total_bytes"].count > 0 {
        Text("\(Format.bytes(row["total_bytes"].count)) in all. Nothing is copied until you approve.")
          .font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink3)
      }
      Text(id).font(.mono(11)).foregroundStyle(Palette.ink4).textSelection(.enabled)
      Button {
        store.perform {
          store.review = try await store.rpc("plan.get", ["plan_id": .string(id)])
          store.section = Place.transfer.rawValue
        }
      } label: {
        Label("Review & approve", systemImage: "arrow.right").labelStyle(TrailingIcon())
      }
      .buttonStyle(.primary)
      .padding(.top, Space.s2)
    }
    .padding(Space.s6)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(GlassSurface(radius: 22))
  }
}

/// A receipt: what was written, its digest, and the way to check it on disk again.
struct ReceiptView: View {
  let store: Store
  let row: JSON
  /// Whether Verify on disk is the screen's one primary action.
  var primary = true
  @State private var doc: JSON = .null
  @State private var verification: JSON = .null
  @State private var path = ""
  @State private var failure = ""
  @State private var busy = false
  var ref: JSON { ["receipt_id": row["receipt_id"]] }

  var body: some View {
    VStack(alignment: .leading, spacing: Space.s4) {
      HStack(alignment: .firstTextBaseline) {
        Text("Receipt").font(.inter(TypeScale.md, .semibold)).foregroundStyle(Palette.bone)
        Text(row["receipt_id"].text).font(.mono(11)).foregroundStyle(Palette.ink4).textSelection(.enabled)
        Spacer()
      }
      VStack(spacing: 8) {
        fact("Checked with", doc["checksum_algo"].text.uppercased())
        HStack(alignment: .firstTextBaseline, spacing: Space.s4) {
          Text("Digest").font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
            .frame(width: 96, alignment: .leading)
          Detail(text: doc["receipt_digest"].text, color: Palette.ink1)
          Spacer(minLength: 0)
          Button {
            copyText(doc["receipt_digest"].text)
          } label: {
            Image(systemName: "doc.on.doc").font(.system(size: 10, weight: .medium))
          }
          .buttonStyle(.park(.quiet, .small, disc: true))
          .help("Copy digest")
        }
        ForEach(doc["targets"].rows, id: \.self) { target in
          fact(target["label"].text, readback(target["readback_mode"].text) + " · " + StatusPill.words(target["status"].text))
        }
        if !path.isEmpty { fact("Saved to", path, mono: true) }
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
          Label(busy ? "Checking…" : "Verify on disk", systemImage: "checkmark.shield")
        }
        .buttonStyle(primary ? .park(.primary) : .park(.standard))
        .disabled(busy || doc == .null)
        if verification != .null {
          StatusPill(word: verification["clean"] == .bool(true) ? "clean" : "needs_review")
        } else {
          Text("Reads every file again and compares it with this receipt.").font(.inter(TypeScale.xs))
            .foregroundStyle(Palette.ink3)
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
            HStack {
              Detail(text: file["dest_rel_path"].text, color: Palette.ink1)
              Spacer()
              Text(Format.bytes(file["size"].count)).font(.inter(11)).foregroundStyle(Palette.ink4)
            }
            .padding(.vertical, 5)
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
    .task {
      path = row["exported_path"].text
      failure = row["export_error"].text
      do { doc = try await store.rpc("receipt.get", ref) } catch {
        failure = error.localizedDescription
      }
    }
  }

  /// How a file was read back, in plain words.
  private func readback(_ mode: String) -> String {
    switch mode {
    case "f_nocache", "fadvise_dontneed": "Read back from disk"
    case "remote_readback": "Read back remotely"
    default: mode.isEmpty ? "Read back" : display(mode)
    }
  }

  private func fact(_ name: String, _ value: String, mono: Bool = false) -> some View {
    HStack(alignment: .firstTextBaseline, spacing: Space.s4) {
      Text(name).font(.inter(TypeScale.xs)).foregroundStyle(Palette.ink3)
        .frame(width: 96, alignment: .leading).lineLimit(1)
      if mono {
        Detail(text: value, color: Palette.ink1)
      } else {
        Text(value.isEmpty ? "—" : value).font(.inter(TypeScale.xs, .medium)).foregroundStyle(Palette.ink1)
          .lineLimit(1)
      }
      Spacer(minLength: 0)
    }
  }
}
