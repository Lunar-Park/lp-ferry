import CoreText
import FerryCore
import SwiftUI
import UserNotifications

@main struct FerryApp: App {
  @State private var store: Store
  init() {
    if let url = Bundle.main.url(forResource: "Inter", withExtension: "woff2") {
      CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
    }
    let store = Store()
    _store = State(initialValue: store)
    store.onApproval = { event in
      guard !NSApp.isActive else { return }
      let content = UNMutableNotificationContent()
      content.title = "Ferry needs your approval"
      content.body =
        "Triggered by " + event["actor"].text.replacingOccurrences(of: "agent:", with: "")
      UNUserNotificationCenter.current().add(
        UNNotificationRequest(identifier: event["plan_id"].text, content: content, trigger: nil))
    }
  }
  var body: some Scene {
    Window("File Ferry", id: "main") {
      Host(store: store).ignoresSafeArea(.container, edges: .top).frame(
        minWidth: 800, minHeight: 560
      )
      .task {
        store.start()
        if Snapshots.directory == nil && !store.fixture && Bundle.main.bundleIdentifier != nil {
          _ = try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert]
          )
        }
      }
    }.windowStyle(.hiddenTitleBar).defaultSize(width: 1160, height: 790)
    MenuBarExtra {
      MenuStatus(store: store)
    } label: {
      Image(systemName: "ferry")
      if !store.approvals.isEmpty {
        Text("\(store.approvals.count) pending")
      } else if !store.progress.isEmpty {
        Text("\(store.progress.count)")
      }
    }
  }
}
struct MenuStatus: View {
  let store: Store
  @Environment(\.openWindow) var openWindow
  var body: some View {
    Text(store.online ? "Daemon online" : "Connecting to Ferry")
    Button("Activity · \(store.progress.count) active · \(store.approvals.count) awaiting approval")
    {
      store.section = "Activity"
      openWindow(id: "main")
      NSApp.activate(ignoringOtherApps: true)
    }
    Button("Quit Ferry") { NSApp.terminate(nil) }
  }
}
struct Host: NSViewRepresentable {
  let store: Store
  func makeNSView(context: Context) -> NSHostingView<MainShell> {
    let host = FirstMouseHostingView(rootView: MainShell(store: store))
    if let directory = Snapshots.directory {
      Task { await Snapshots.capture(host, store: store, into: directory) }
    }
    return host
  }
  func updateNSView(_ view: NSHostingView<MainShell>, context: Context) {}
}
