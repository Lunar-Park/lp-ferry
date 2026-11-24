import SwiftUI

struct WindowDragArea: NSViewRepresentable {
  func makeNSView(context: Context) -> NSView { DragView() }
  func updateNSView(_ nsView: NSView, context: Context) {}

  private final class DragView: NSView {
    override var mouseDownCanMoveWindow: Bool { true }
  }
}

/// A hosting view that acts on the click that brings its window forward, instead
/// of spending it on activation.
final class FirstMouseHostingView<Content: View>: NSHostingView<Content> {
  override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }
  // Left to itself, a press anywhere in the title bar moves the window, which
  // swallows a drag on a tab. Only the empty strip (WindowDragArea) moves it.
  override var mouseDownCanMoveWindow: Bool { false }
}

extension View {
  /// Overlay scrollers on the scroll view this content sits in: nothing at rest, only the thin
  /// pill while scrolling, even with a mouse attached (where macOS otherwise shows a track).
  /// Put it on the scroll view's content.
  func overlayScrollers() -> some View { background(OverlayScrollers()) }
}

/// Reaches the `NSScrollView` behind a SwiftUI `ScrollView` and keeps it on the overlay style.
/// AppKit puts a scroll view back to the system's style when that preference changes (a mouse
/// plugged in, the setting flipped), and SwiftUI may re-apply its own; both are undone.
private struct OverlayScrollers: NSViewRepresentable {
  func makeNSView(context: Context) -> Probe { Probe() }
  func updateNSView(_ view: Probe, context: Context) { view.apply() }

  final class Probe: NSView {
    private weak var scrollView: NSScrollView?
    private var styleWatch: NSKeyValueObservation?

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidMoveToWindow() {
      super.viewDidMoveToWindow()
      NotificationCenter.default.removeObserver(self)
      guard window != nil else {
        styleWatch = nil
        return
      }
      NotificationCenter.default.addObserver(
        self, selector: #selector(preferenceChanged),
        name: NSScroller.preferredScrollerStyleDidChangeNotification, object: nil)
      apply()
    }

    @objc private func preferenceChanged() {
      // AppKit resets the style after posting; set ours once it has.
      DispatchQueue.main.async { [weak self] in self?.apply() }
    }

    func apply() {
      guard let found = enclosingScrollView else {
        // Not in the hierarchy yet: try again once SwiftUI has placed it.

        return
      }
      if found !== scrollView {
        scrollView = found
        styleWatch = found.observe(\.scrollerStyle, options: [.new]) { view, _ in
          guard view.scrollerStyle != .overlay else { return }
          DispatchQueue.main.async { view.scrollerStyle = .overlay }
        }
      }
      if found.scrollerStyle != .overlay { found.scrollerStyle = .overlay }
      found.autohidesScrollers = true
    }

    deinit { NotificationCenter.default.removeObserver(self) }
  }
}
