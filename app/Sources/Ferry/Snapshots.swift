import AppKit
import FerryCore

/// Render the application's own hosting view. The review gate never needs screen-recording access.
@MainActor enum Snapshots {
  static var directory: URL? {
    ProcessInfo.processInfo.environment["FERRY_SNAPSHOT_DIR"].map {
      URL(fileURLWithPath: $0, isDirectory: true)
    }
  }
  static func capture(_ view: NSView, store: Store, into directory: URL) async {
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
      let scenes =
        store.fixture
        ? ["Transfer", "Plan", "Activity", "Machines & Drives", "Presets & Destinations"]
        : ["Transfer"]
      for scene in scenes {
        store.review = scene == "Plan" ? try await store.rpc("plan.create") : nil
        store.section = scene == "Plan" ? "Transfer" : scene
        try await Task.sleep(for: .milliseconds(450))
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
}
