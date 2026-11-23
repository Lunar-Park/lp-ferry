import FerryCore
import SwiftUI

let policies = ["keep_both", "skip_identical", "needs_review"]
struct Workspace: View {
  @Bindable var store: Store
  @State private var busy = false
  var body: some View {
    if let plan = store.review {
      PlanReview(store: store, plan: plan).id(plan["plan_id"])
    } else {
      HStack(alignment: .top, spacing: 12) {
        Endpoints(store: store, source: true)
        Endpoints(store: store, source: false)
      }
      Panel(title: "Transfer settings") {
        Picker("Organization", selection: $store.preset) {
          Text("Destination default").tag("")
          ForEach(store.presets, id: \.self) { Text($0["name"].text).tag($0["id"].text) }
        }
        Picker("Conflict policy", selection: $store.policy) {
          ForEach(policies, id: \.self) {
            Text($0.replacingOccurrences(of: "_", with: " ").capitalized).tag($0)
          }
        }
        Picker("Checksum", selection: $store.checksum) {
          Text("SHA-256").tag("sha256")
          Text("BLAKE3").tag("blake3")
        }
        ForEach(store.sources, id: \.self) { row in
          let label = row.label
          TextField(
            "Camera role for \(label) (optional)",
            text: Binding(
              get: { store.roles[label] ?? "" },
              set: { store.roles[label] = $0 }))
        }
      }
      PillButton(
        title: busy ? "Planning…" : "Build Plan", role: .primary,
        enabled: store.online && !busy && !store.sources.isEmpty && !store.targets.isEmpty
      ) {
        busy = true
        let request = PlanRequest(workspace: store)
        store.perform {
          defer { busy = false }
          store.review = try await store.rpc("plan.create", request.params)
        }
      }
    }
  }
}

struct Endpoints: View {
  let store: Store
  let source: Bool
  @State private var binding: JSON?
  @State private var peer: JSON?
  @State private var path = ""
  var entries: [Location] { source ? store.sources : store.targets }
  func add(_ row: Location) {
    guard !entries.contains(row) else { return }
    if source { store.sources.append(row) } else { store.targets.append(row) }
  }
  func folder(_ path: String, peerID: JSON? = nil) {
    guard !path.isEmpty else { return }
    add(Location(path: path, peerID: peerID?.text))
  }
  var body: some View {
    Panel(title: source ? "Sources" : "Destinations") {
      Image(systemName: source ? "folder" : "externaldrive").font(.system(size: 24))
        .foregroundStyle(Palette.ink3)
      if entries.isEmpty {
        Text(source ? "Drop source folders here" : "Drop destination folders here").foregroundStyle(
          Palette.ink3)
      }
      ForEach(entries, id: \.self) { row in
        HStack {
          VStack(alignment: .leading) {
            Text(row.label).font(.inter(16, .semibold))
            Detail(
              text: [
                row.peer_id ?? "", row.path ?? row.destination_id ?? "",
              ].filter { !$0.isEmpty }.joined(separator: " · "))
          }
          Button {
            if source {
              store.sources.removeAll { $0 == row }
            } else {
              store.targets.removeAll { $0 == row }
            }
          } label: {
            Image(systemName: "minus.circle")
          }.buttonStyle(.plain)
        }
      }
      Menu(source ? "Add source" : "Add destination") {
        Button("Choose folders…") { for url in pickFolders() { folder(url.path) } }
        ForEach(store.drives, id: \.self) { row in
          Button(row["mount_path"].text) { folder(row["mount_path"].text) }
        }
        if !source {
          ForEach(store.destinations, id: \.self) { row in
            let state = row["resolution"]["state"].text
            Button(row["name"].text + " · " + state) {
              if state == "needs_confirmation" {
                binding = row
              } else {
                add(Location(destinationID: row["id"].text, label: row["name"].text))
              }
            }.disabled(!["available", "needs_confirmation", "remote"].contains(state))
          }
        }
        ForEach(store.peers, id: \.self) { row in
          Button("\(row["name"].text)…") {
            path = ""
            peer = row
          }
        }
      }.fixedSize()
    }.confirmationDialog(
      "Use this destination binding?",
      isPresented: Binding(get: { binding != nil }, set: { if !$0 { binding = nil } }),
      presenting: binding
    ) { row in
      if let candidate = row["resolution"]["candidates"].rows.first,
        row["resolution"]["candidates"].rows.count == 1
      {
        Button("Use " + candidate.text) {
          add(Location(destinationID: row["id"].text, label: row["name"].text, confirmed: true))
        }
      }
    } message: { row in
      Text(row["resolution"]["reason"].text)
    }.dropDestination(for: URL.self) { urls, _ in
      for url in urls where url.isFileURL { folder(url.path) }
      return !urls.isEmpty
    }.sheet(isPresented: Binding(get: { peer != nil }, set: { if !$0 { peer = nil } })) {
      SheetPage(title: peer?["name"].text ?? "") {
        TextField("Absolute path on peer", text: $path)
        PillButton(title: "Add path", role: .primary, enabled: path.hasPrefix("/")) {
          folder(path, peerID: peer?["id"])
          peer = nil
        }
      }
    }
  }
}
