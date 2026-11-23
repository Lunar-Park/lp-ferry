import FerryCore
import SwiftUI

struct Dashboard: View {
  let store: Store
  var body: some View {
    HStack(spacing: 20) {
      metric("Active transfers", store.progress.count, Palette.bone)
      metric(
        "Approval / attention",
        store.approvals.count
          + store.transfers.filter { ["partial", "needs_attention"].contains($0["status"].text) }
          .count, Palette.warn)
      metric(
        "Recently verified", store.receipts.filter { $0["status"] == "completed" }.count,
        Palette.bone
      )
    }.padding(.vertical, 10)
    Machines(store: store, overview: true)
    Panel(
      title: "Recent transfers", actionTitle: "View all in Activity  →",
      action: { store.section = "Activity" }
    ) {
      Grid(alignment: .leading, horizontalSpacing: 18, verticalSpacing: 11) {
        GridRow {
          ColumnLabel("Transfer").frame(maxWidth: .infinity, alignment: .leading)
          ColumnLabel("State")
          ColumnLabel("Throughput / files")
        }
        ForEach(Array(store.transfers.prefix(5)), id: \.self) { row in
          Divider()
          GridRow {
            Text(row["id"].text).lineLimit(1).truncationMode(.middle)
            StateLabel(word: row["status"].text)
            let targets = store.progress[row["id"].text]?["targets"].rows ?? []
            let bytes = targets.reduce(0) { $0 + $1["write_bps"].double }
            VStack(alignment: .leading, spacing: 3) {
              Text(targets.isEmpty ? "—" : Format.bytes(Int64(bytes)) + "/s")
              if let t = targets.first, t["total_files"] != .null {
                Text(
                  "\(t["done_files"].count) / \(t["total_files"].count) files · \(t["label"].text)"
                ).font(.inter(11)).foregroundStyle(Palette.ink3)
              }
            }
          }
        }
      }
      if store.transfers.isEmpty {
        Text("Transfer history is empty.").foregroundStyle(Palette.ink3)
      }
    }
  }
  func metric(_ title: String, _ count: Int, _ color: Color) -> some View {
    VStack(alignment: .leading, spacing: 6) {
      ColumnLabel(title)
      Text(String(count)).font(.inter(52, .semibold)).foregroundStyle(color)
    }.frame(maxWidth: .infinity, alignment: .leading)
  }
}
