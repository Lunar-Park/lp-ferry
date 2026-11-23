import FerryCore
import SwiftUI

/// The window: the living room under everything, the rail of three glass discs on the
/// traffic lights' axis, and the page beside it under one open head band.
struct MainShell: View {
  @Bindable var store: Store
  @Bindable var chrome: Chrome

  var body: some View {
    AmbientRoom {
      ZStack(alignment: .topLeading) {
        Ground()
        HStack(spacing: 0) {
          Rail(store: store, chrome: chrome)
          page
            .padding(.leading, Page.gutter)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
      }
    }
    .background(WindowChrome())
    .ignoresSafeArea()
    .environment(\.ferryStore, store)
    .environment(\.chrome, chrome)
    .foregroundStyle(Palette.ink1)
    .font(.inter(TypeScale.base))
    .tint(Palette.accent)
    .textFieldStyle(ParkFieldStyle())
    .preferredColorScheme(.dark)
    .sheet(isPresented: $chrome.doctor) { DoctorSheet(store: store) }
    .alert(
      "Ferry",
      isPresented: Binding(get: { store.error != nil }, set: { if !$0 { store.error = nil } })
    ) {
      Button("OK") { store.error = nil }
    } message: {
      Text(store.error ?? "")
    }
  }

  @ViewBuilder private var page: some View {
    switch Place(rawValue: store.section) ?? .transfer {
    case .transfer:
      if let plan = store.review {
        PlanReview(store: store, chrome: chrome, plan: plan).id(plan["plan_id"])
      } else {
        TransferPage(store: store, chrome: chrome)
      }
    case .activity: Activity(store: store, chrome: chrome)
    case .devices: Devices(store: store, chrome: chrome)
    }
  }
}

/// A page: its head in the band, then its content scrolling under it on the open room. The
/// content is told how wide it is, so a page can fold two columns into one.
struct PageFrame<Head: View, Content: View>: View {
  var scrolls = true
  @ViewBuilder var head: Head
  @ViewBuilder var content: (CGFloat) -> Content

  var body: some View {
    VStack(alignment: .leading, spacing: 0) {
      head
      GeometryReader { box in
        if scrolls {
          GlassScroll {
            VStack(alignment: .leading, spacing: 0) { content(box.size.width) }
              .padding(.top, Space.s3)
              .padding(.bottom, Space.s8)
              .frame(maxWidth: .infinity, alignment: .leading)
          }
        } else {
          content(box.size.width)
        }
      }
    }
    .padding(.trailing, Page.gutter)
  }
}

/// Ferry Doctor's checks, one row each, in a sheet.
struct DoctorSheet: View {
  let store: Store
  @State private var report: JSON = .null
  @State private var failure = ""

  var body: some View {
    SheetPage(title: "Ferry Doctor") {
      if report == .null && failure.isEmpty {
        HStack(spacing: Space.s2) {
          ProgressView().controlSize(.small)
          Text("Running checks…").foregroundStyle(Palette.ink3)
        }
      }
      if !failure.isEmpty { Text(failure).foregroundStyle(Palette.warn) }
      VStack(spacing: 0) {
        ForEach(Array(report.rows.enumerated()), id: \.offset) { index, row in
          if index > 0 { Hairline() }
          HStack(alignment: .top, spacing: Space.s3) {
            StatusPill(word: row["ok"] == .bool(true) ? "passed" : "failed")
            VStack(alignment: .leading, spacing: 4) {
              Text(row["name"].text).font(.inter(TypeScale.sm, .semibold))
              Text(row["detail"].text).font(.mono()).foregroundStyle(Palette.ink3)
                .textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
          }
          .padding(.vertical, Space.s3)
        }
      }
      if report != .null {
        Button {
          copyText(report.pretty)
        } label: {
          Label("Copy diagnostics", systemImage: "doc.on.doc")
        }
        .buttonStyle(.standard)
      }
    }
    .task {
      do { report = try await store.rpc("doctor.run") } catch {
        failure = error.localizedDescription
      }
    }
  }
}
