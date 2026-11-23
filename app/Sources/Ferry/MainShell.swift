import FerryCore
import SwiftUI

struct MainShell: View {
  @Bindable var store: Store
  @State private var doctor: JSON?
  @State private var slim = false
  @AppStorage("navigationWidth") private var width = 224.0
  @State private var dragStart: Double?
  static let sections = [
    "Dashboard", "Transfer", "Activity", "Machines & Drives", "Presets & Destinations",
  ]
  let symbols = [
    "square.grid.2x2", "arrow.left.arrow.right", "waveform.path.ecg",
    "externaldrive.connected.to.line.below", "slider.horizontal.3",
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
          }
          .buttonStyle(.plain).help("Toggle navigation")
          Text("File Ferry").font(.inter(12, .medium))
          Spacer()
          Button {
            store.perform { doctor = try await store.rpc("doctor.run") }
          } label: {
            HStack(spacing: 6) {
              Circle().fill(store.online ? Palette.ok : Palette.warn).frame(width: 6, height: 6)
              Text(
                store.online
                  ? "DAEMON  ·  PROTOCOL V\(store.status["protocol"].count)" : "DAEMON  ·  OFFLINE"
              )
              .font(.system(size: 10, weight: .medium, design: .monospaced))
            }.padding(.horizontal, 10).padding(.vertical, 4).background(
              Palette.plate, in: Capsule()
            )
            .overlay(Capsule().strokeBorder(Palette.rule2))
          }.buttonStyle(.plain).help("Run Ferry Doctor").popover(
            isPresented: Binding(get: { doctor != nil }, set: { if !$0 { doctor = nil } })
          ) {
            VStack(alignment: .leading, spacing: 10) {
              Text("Ferry Doctor").font(.inter(18, .semibold))
              ScrollView {
                VStack(alignment: .leading) {
                  ForEach(doctor?.rows ?? [], id: \.self) { row in
                    Panel(title: row["name"].text) {
                      StateLabel(word: row["ok"] == .bool(true) ? "passed" : "failed")
                      Detail(text: row["detail"].text)
                    }
                  }
                }
              }
              PillButton(title: "Copy diagnostics") { copyText(doctor?.pretty ?? "") }
            }.padding(16).frame(width: 360, height: 340).background(FerryRoom())
          }
        }.padding(.leading, 78).padding(.trailing, 16).frame(height: 36).background(
          WindowDragArea()
        ).background(Palette.rail)
        Divider()
        HStack(spacing: 0) {
          VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 9) {
              ShipMark().fill(Palette.brand).frame(width: compact ? 34 : 42, height: 27)
              if !compact {
                VStack(alignment: .leading, spacing: 3) {
                  (Text("FILE-").foregroundColor(Palette.bone)
                    + Text("FERRY").foregroundColor(Palette.brand))
                    .font(.inter(16, .bold)).tracking(1.4)
                  Text("Media manager").font(.inter(11)).foregroundStyle(Palette.ink3)
                }
              }
            }.padding(.horizontal, compact ? 4 : 7).padding(.bottom, 26)
            ForEach(Array(Self.sections.enumerated()), id: \.offset) { i, section in
              if i == 0 || i == 3 {
                if !compact {
                  ColumnLabel(i == 0 ? "Workspace" : "Library").padding(.leading, 10).padding(
                    .top, i == 0 ? 0 : 24
                  ).padding(.bottom, 5)
                } else if i == 3 {
                  Divider().padding(.vertical, 10)
                }
              }
              Button {
                store.section = section
              } label: {
                HStack(spacing: 10) {
                  Image(systemName: symbols[i]).frame(width: 18).foregroundStyle(
                    store.section == section ? Palette.brand : Palette.ink3)
                  if !compact {
                    Text(section).font(.inter(13, .medium)).lineLimit(1).minimumScaleFactor(0.9)
                    Spacer()
                    if section == "Activity", !store.approvals.isEmpty {
                      Text(String(store.approvals.count)).font(.inter(10, .semibold)).padding(
                        .horizontal, 5
                      ).padding(.vertical, 1).background(Palette.warn.opacity(0.12), in: Capsule())
                        .foregroundStyle(Palette.warn).help("Pending approval")
                    }
                  }
                }.padding(10).frame(maxWidth: .infinity, alignment: .leading)
                  .background(
                    RoundedRectangle(cornerRadius: 6).fill(
                      store.section == section ? Palette.top : .clear)
                  )
                  .overlay(alignment: .leading) {
                    if store.section == section {
                      Capsule().fill(Palette.accent).frame(width: 2, height: 17)
                    }
                  }
              }.buttonStyle(.plain).help(section).accessibilityLabel(section)
            }
            Spacer()
            Divider()
            Label {
              if !compact {
                Text(store.fixture ? "Fixture session" : store.status["name"].text).lineLimit(1)
              }
            } icon: {
              Image(systemName: "desktopcomputer")
            }
            .font(.inter(12)).foregroundStyle(Palette.ink3).padding(10)
          }.padding(.horizontal, compact ? 6 : 10).padding(.vertical, 24)
            .frame(width: compact ? 54 : width)
            .background(
              LinearGradient(
                colors: [Palette.rail, Palette.rail, Palette.onAccent], startPoint: .top,
                endPoint: .bottom))
          Rectangle().fill(Palette.rule2).frame(width: 1).contentShape(Rectangle().inset(by: -3))
            .gesture(
              DragGesture(coordinateSpace: .global).onChanged { value in
                if dragStart == nil { dragStart = width }
                width = min(300, max(206, (dragStart ?? width) + value.translation.width))
              }.onEnded { _ in dragStart = nil })
          if store.section == "Transfer", let plan = store.review {
            PlanReview(store: store, plan: plan).id(plan["plan_id"])
          } else {
            ScrollView {
              VStack(alignment: .leading, spacing: 18) {
                Text(store.section).font(.inter(31, .semibold)).foregroundStyle(Palette.bone)
                switch store.section {
                case "Dashboard": Dashboard(store: store)
                case "Activity": Activity(store: store)
                case "Machines & Drives": Machines(store: store)
                case "Presets & Destinations": Library(store: store)
                default: Workspace(store: store)
                }
              }.overlayScrollers().padding(compact ? 22 : 28).frame(
                maxWidth: .infinity, alignment: .leading)
            }.id(store.section)
          }
        }
      }
    }.ignoresSafeArea(.container, edges: .top).background(FerryRoom()).foregroundStyle(Palette.ink1)
      .tint(Palette.brand)
      .buttonStyle(.plain).textFieldStyle(StudioField()).font(.inter(14)).preferredColorScheme(
        .dark
      )
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
