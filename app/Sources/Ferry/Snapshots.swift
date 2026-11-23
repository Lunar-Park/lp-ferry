import AppKit
import FerryCore

/// Render the application's own hosting view. The review gate never needs screen-recording
/// access; the room is drawn into an image (`RoomRenderer.snapshot`), so the glass is real.
@MainActor enum Snapshots {
  static var directory: URL? {
    ProcessInfo.processInfo.environment["FERRY_SNAPSHOT_DIR"].map {
      URL(fileURLWithPath: $0, isDirectory: true)
    }
  }

  /// The screens a review looks at, each set up from the fixture's state.
  static let fixtureScenes = [
    "Transfer-Idle", "Transfer-Ready", "Transfer-Options", "Transfer-Review", "Activity",
    "Activity-Receipt", "Devices", "Devices-Destinations",
  ]

  static func capture(_ view: NSView, store: Store, chrome: Chrome, into directory: URL) async {
    do {
      try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
      for _ in 0..<100 where !store.online || view.window == nil {
        try await Task.sleep(for: .milliseconds(100))
      }
      guard store.online, let window = view.window else {
        throw CocoaError(.executableRuntimeMismatch)
      }
      let width = Double(ProcessInfo.processInfo.environment["FERRY_SNAPSHOT_WIDTH"] ?? "") ?? 1160
      window.setContentSize(NSSize(width: max(800, width), height: 790))
      let progress = store.progress
      let approvals = store.approvals
      for scene in store.fixture ? fixtureScenes : ["Transfer-Idle"] {
        try await set(scene, store: store, chrome: chrome, progress: progress, approvals: approvals)
        try await Task.sleep(for: .milliseconds(500))
        view.layoutSubtreeIfNeeded()
        guard let bitmap = view.bitmapImageRepForCachingDisplay(in: view.bounds) else {
          throw CocoaError(.fileWriteUnknown)
        }
        view.cacheDisplay(in: view.bounds, to: bitmap)
        guard let png = bitmap.representation(using: .png, properties: [:]) else {
          throw CocoaError(.fileWriteUnknown)
        }
        try png.write(to: directory.appendingPathComponent(scene + ".png"))
      }
      NSApp.terminate(nil)
    } catch {
      FileHandle.standardError.write(Data("Snapshot failed: \(error)\n".utf8))
      exit(1)
    }
  }

  private static func set(
    _ scene: String, store: Store, chrome: Chrome, progress: [String: JSON],
    approvals: [String: JSON]
  ) async throws {
    let idle = scene == "Transfer-Idle"
    store.progress = idle ? [:] : progress
    store.approvals = idle ? [:] : approvals
    store.review = nil
    chrome.optionsOpen = scene == "Transfer-Options"
    if store.fixture {
      let ready = scene.hasPrefix("Transfer") && !idle
      store.sources = ready ? [Location(path: "/Volumes/A004")] : []
      store.targets = ready ? [Location(destinationID: "archive", label: "Studio archive")] : []
    }
    switch scene {
    case "Transfer-Review":
      store.section = Place.transfer.rawValue
      store.review = try await store.rpc("plan.create")
    case "Activity":
      chrome.activityFilter = "All"
      chrome.activitySelection = "tr_day04"
      store.section = Place.activity.rawValue
    case "Activity-Receipt":
      chrome.activityFilter = "Completed"
      chrome.activitySelection = "tr_day03"
      store.section = Place.activity.rawValue
    case "Devices":
      chrome.devicesTab = DevicesTab.drives.rawValue
      store.section = Place.devices.rawValue
    case "Devices-Destinations":
      chrome.devicesTab = DevicesTab.places.rawValue
      store.section = Place.devices.rawValue
    default:
      store.section = Place.transfer.rawValue
    }
  }
}
