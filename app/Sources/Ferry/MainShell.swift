import FerryCore
import SwiftUI

struct MainShell: View {
  @Bindable var store: Store
  @State private var doctor: JSON?
  @State private var slim = false
  @AppStorage("navigationWidth") private var width = 230.0
  @State private var dragStart: Double?
  @State private var dismissed = Set<String>()
  static let sections = ["Transfer", "Activity", "Machines & Drives", "Presets & Destinations"]
  let symbols = [
    "arrow.right.square", "waveform.path.ecg", "externaldrive.connected.to.line.below",
    "slider.horizontal.3",
  ]
  var body: some View {
    GeometryReader { proxy in
      let compact = slim || proxy.size.width < 930
      VStack(spacing: 0) {
        HStack(spacing: 12) {
          Button {
            slim.toggle()
          } label: {
            Image(systemName: "sidebar.left")
          }.buttonStyle(.plain)
          Text("File Ferry").font(.inter(13, .medium))
          Spacer()
          if store.fixture { Text("Fixture session").foregroundStyle(Palette.ink4) }
        }.padding(.leading, 78).padding(.trailing, 20).frame(height: 36).background(
          WindowDragArea())
        Divider()
        HStack(spacing: 0) {
          VStack(alignment: .leading, spacing: 6) {
            HStack {
              Image(systemName: "shippingbox.fill").foregroundStyle(Palette.accent)
              if !compact { Text("File Ferry").font(.inter(20, .semibold)) }
            }.padding(.horizontal, 10).padding(.bottom, 24)
            ForEach(Array(Self.sections.enumerated()), id: \.offset) { i, section in
              Button {
                store.section = section
              } label: {
                HStack(spacing: 10) {
                  Image(systemName: symbols[i]).frame(width: 18)
                  if !compact {
                    Text(section).font(.inter(13, .medium))
                    Spacer()
                  }
                }.padding(10).frame(maxWidth: .infinity, alignment: .leading)
                  .background(
                    RoundedRectangle(cornerRadius: 9).fill(
                      store.section == section ? Palette.selSoft : .clear))
              }.buttonStyle(.plain).help(section).accessibilityLabel(section)
            }
            Spacer()
            Button {
              store.perform { doctor = try await store.rpc("doctor.run") }
            } label: {
              if compact {
                Image(systemName: "stethoscope")
              } else {
                VStack(alignment: .leading, spacing: 6) {
                  StateLabel(word: store.online ? "online" : "offline")
                  Text(
                    store.online
                      ? "\(store.status["name"].text) · \(store.status["version"].text)"
                      : store.connectionNote
                  )
                  .font(.inter(11)).foregroundStyle(Palette.ink4)
                }
              }
            }.buttonStyle(.plain).padding(8).help("Run Doctor")
          }.padding(.horizontal, compact ? 6 : 12).padding(.vertical, 22).frame(
            width: compact ? 52 : width)
          Rectangle().fill(Palette.rule1).frame(width: 1).padding(.horizontal, 3).contentShape(
            Rectangle()
          )
          .gesture(
            DragGesture(coordinateSpace: .global).onChanged { value in
              if dragStart == nil { dragStart = width }
              width = min(300, max(210, (dragStart ?? width) + value.translation.width))
            }.onEnded { _ in dragStart = nil })
          ScrollView {
            VStack(alignment: .leading, spacing: 18) {
              if store.section != "Activity",
                let id = store.approvals.keys.sorted().first(where: { !dismissed.contains($0) })
              {
                HStack {
                  StateLabel(word: "Awaiting approval")
                  Spacer()
                  Button("Review") { store.section = "Activity" }
                  Button("Dismiss") { dismissed.insert(id) }
                }.buttonStyle(.plain).padding(12).paneGlass(glow: Palette.warn)
              }
              Text(store.section).font(.inter(30, .semibold))
              switch store.section {
              case "Activity": Activity(store: store)
              case "Machines & Drives": Machines(store: store)
              case "Presets & Destinations": Library(store: store)
              default: Workspace(store: store)
              }
            }.overlayScrollers().padding(26).frame(maxWidth: .infinity, alignment: .leading)
          }
        }
      }
    }.background(FerryRoom()).foregroundStyle(Palette.ink1).tint(Palette.ink2)
      .font(.inter(14)).monospacedDigit().preferredColorScheme(.dark)
      .sheet(isPresented: Binding(get: { doctor != nil }, set: { if !$0 { doctor = nil } })) {
        SheetPage(title: "Ferry Doctor") {
          ForEach(doctor?.rows ?? [], id: \.self) { row in
            Panel(title: row["name"].text) {
              StateLabel(word: row["ok"] == .bool(true) ? "passed" : "failed")
              Detail(text: row["detail"].text)
            }
          }
          PillButton(title: "Copy diagnostics") { copyText(doctor?.pretty ?? "") }
        }
      }
      .alert(
        "Ferry",
        isPresented: Binding(get: { store.error != nil }, set: { if !$0 { store.error = nil } })
      ) {
        Button("OK") { store.error = nil }
      } message: {
        Text(store.error ?? "")
      }
  }
}
