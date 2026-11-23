import AppKit
import FerryCore
import SwiftUI

// The window's spatial shell, after the Lunar Park style kit
// (park/apple/Sources/ParkNative/Shell.swift): glass discs floating on the room down the
// traffic lights' axis and one open head band over every page. Ferry has three places, so
// the rail has three discs; the head carries nothing but the page's title until the daemon
// needs attention.

/// Where a page sits beside the rail. One gutter runs from the window's edge to the rail's
/// discs, from the discs to the page, and from the page to the window's right edge.
enum Page {
  static let gutter: CGFloat = 28
  /// The rail's centre line: the discs (40 wide) one gutter from the window's edge, which is
  /// also the traffic lights' centre.
  static let axis: CGFloat = gutter + 20
  /// The rail's column; the page's gutter follows it.
  static let rail: CGFloat = axis * 2 - gutter
  /// Where every page's content starts, from the window's left edge.
  static let left: CGFloat = rail + gutter
}

/// The window's top band, one height on every page: the traffic lights and the page's title
/// sit on its midline, as a Mac toolbar window's do.
enum Head {
  static let band: CGFloat = 64
  /// The height of every control in the band.
  static let control: CGFloat = 32
  /// Where a page's content starts.
  static var content: CGFloat { band }
}

/// Ferry's three places: start a copy, watch and check copies, set up where things live.
enum Place: String, CaseIterable, Identifiable {
  case transfer = "Transfer"
  case activity = "Activity"
  case devices = "Devices"

  var id: String { rawValue }
  var glyph: LineIcon? {
    switch self {
    case .transfer: nil  // the ship
    case .activity: .activity
    case .devices: .machines
    }
  }
  var shortcut: KeyEquivalent {
    switch self {
    case .transfer: "1"
    case .activity: "2"
    case .devices: "3"
    }
  }
}

/// What the pages share that the daemon does not own.
@MainActor @Observable final class Chrome {
  var activityFilter = "All"
  /// The Activity record to open on arrival (a transfer or plan id).
  var activitySelection = ""
  var devicesTab = DevicesTab.drives.rawValue
  var optionsOpen = false
  var doctor = false
  let routes = Routes()

  func open(_ id: String, in store: Store) {
    activityFilter = "All"
    activitySelection = id
    store.section = Place.activity.rawValue
  }
}

/// The rail: Ferry's three places as glass discs, then Ferry Doctor at the foot.
struct Rail: View {
  @Bindable var store: Store
  let chrome: Chrome

  var body: some View {
    VStack(spacing: Space.s4) {
      ForEach(Place.allCases) { place in
        RailButton(
          glyph: place.glyph, label: place.rawValue, current: store.section == place.rawValue,
          badge: place == .activity ? store.approvals.count : 0
        ) {
          store.section = place.rawValue
        }
        .keyboardShortcut(place.shortcut, modifiers: .command)
        .help("\(place.rawValue) (⌘\(String(place.shortcut.character)))")
      }
      Spacer(minLength: Space.s4)
      Button {
        chrome.doctor = true
      } label: {
        VStack(spacing: 6) {
          LineIcon.doctor.stroke(
            Palette.ink3, style: StrokeStyle(lineWidth: 1.4, lineCap: .round, lineJoin: .round)
          )
          .frame(width: 18, height: 18)
          Text("Doctor").font(.inter(11)).foregroundStyle(Palette.ink4)
        }
        .contentShape(Rectangle())
      }
      .buttonStyle(.plain)
      .help("Ferry Doctor: check the daemon, disks and paired machines")
      .accessibilityLabel("Ferry Doctor")
    }
    // The traffic lights sit in this column's part of the band; the first disc lines up with
    // the top of the page's content under it.
    .padding(.top, Head.content - 3)
    .padding(.bottom, Space.s6)
    .frame(width: Page.axis * 2)
    .padding(.trailing, -Page.gutter)
  }
}

/// A place on the rail: a disc of the room's glass with a line icon, and the mist disc for the
/// place you are in. Transfer carries the ship.
struct RailButton: View {
  let glyph: LineIcon?
  let label: String
  let current: Bool
  var badge = 0
  let action: () -> Void
  @State private var hovering = false

  var body: some View {
    Button(action: action) {
      VStack(spacing: 7) {
        ZStack {
          if current {
            Circle().fill(Palette.sel)
            Circle().strokeBorder(
              LinearGradient(
                colors: [.white.opacity(0.85), .white.opacity(0)], startPoint: .top,
                endPoint: .center), lineWidth: 1)
          } else {
            GlassSurface(radius: 20, style: hovering ? GlassStyle(smoke: 0, sheen: 1.8) : .control)
          }
          if let glyph {
            glyph.stroke(
              current ? Palette.accentDeep : (hovering ? Palette.ink1 : Palette.ink2),
              style: StrokeStyle(lineWidth: 1.35, lineCap: .round, lineJoin: .round)
            )
            .frame(width: 20, height: 20)
          } else {
            ShipLogo(
              wake: current ? Palette.accentMid : Palette.accent,
              hull: current ? Palette.accentDeep : Palette.bone
            )
            .frame(width: 30, height: 14.5).offset(x: 0.5, y: -0.5)
          }
        }
        .frame(width: 40, height: 40)
        .shadow(color: .black.opacity(current ? 0.35 : 0.23), radius: current ? 2 : 9, y: current ? 1 : 6)
        .overlay(alignment: .topTrailing) {
          if badge > 0 { Badge(count: badge).offset(x: 4, y: -3) }
        }
        .offset(y: hovering && !current ? -1 : 0)
        Text(label).font(.inter(TypeScale.xs, current ? .medium : .regular))
          .foregroundStyle(current || hovering ? Palette.ink1 : Palette.ink3)
      }
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
    .animation(.spring(response: 0.25, dampingFraction: 0.7), value: hovering)
    .onHover { hovering = $0 }
    .accessibilityLabel(badge > 0 ? "\(label), \(badge) waiting" : label)
    .accessibilityAddTraits(current ? .isSelected : [])
  }
}

/// A count on a disc's shoulder.
struct Badge: View {
  let count: Int
  var body: some View {
    Text("\(count)").font(.inter(11, .bold))
      .foregroundStyle(Palette.groundDeep)
      .padding(.horizontal, 4).frame(minWidth: 16, minHeight: 16)
      .background(Capsule().fill(Palette.warn))
      .overlay(Capsule().strokeBorder(Palette.groundDeep.opacity(0.6), lineWidth: 1))
  }
}

/// The page's first line: its title in the band, a quiet note beside it, and whatever the
/// page puts at the trailing edge. When the daemon is away, a calm pill says so.
struct PageHead<Trailing: View>: View {
  let title: String
  var note: String? = nil
  @ViewBuilder var trailing: Trailing

  var body: some View {
    HStack(alignment: .center, spacing: Space.s4) {
      Text(title).font(.inter(TypeScale.xl, .semibold)).tracking(-0.75)
        .foregroundStyle(Palette.bone).lineLimit(1).layoutPriority(1)
      if let note {
        Text(note).font(.inter(TypeScale.md)).foregroundStyle(Palette.ink3).lineLimit(1)
          .padding(.top, 3)
      }
      Spacer(minLength: Space.s4)
      trailing
      HealthPill()
    }
    .frame(height: Head.band)
    .background(WindowDragArea())
  }
}

extension PageHead where Trailing == EmptyView {
  init(title: String, note: String? = nil) {
    self.init(title: title, note: note, trailing: { EmptyView() })
  }
}

private struct StoreKey: EnvironmentKey {
  static let defaultValue: Store? = nil
}

private struct ChromeKey: EnvironmentKey {
  static let defaultValue: Chrome? = nil
}

extension EnvironmentValues {
  var ferryStore: Store? {
    get { self[StoreKey.self] }
    set { self[StoreKey.self] = newValue }
  }
  var chrome: Chrome? {
    get { self[ChromeKey.self] }
    set { self[ChromeKey.self] = newValue }
  }
}

/// Nothing while the daemon is with us; a quiet amber pill while it is not, which opens
/// Ferry Doctor.
struct HealthPill: View {
  @Environment(\.ferryStore) private var store
  @Environment(\.chrome) private var chrome

  var body: some View {
    if let store, !store.online {
      Button {
        chrome?.doctor = true
      } label: {
        HStack(spacing: 7) {
          Circle().fill(Palette.warn).frame(width: 7, height: 7)
          Text("Connecting to Ferry…").font(.inter(13, .medium)).foregroundStyle(Palette.ink2)
        }
        .padding(.horizontal, 12).frame(height: 28)
        .background(Capsule().fill(Palette.warn.opacity(0.10)))
      }
      .buttonStyle(.plain)
      .help(store.connectionNote)
    }
  }
}

/// The window as a Mac toolbar window: the traffic lights centred in the `Head.band` band with
/// a unified toolbar's inset (20 points), on the midline the page's title and the floating
/// discs share. AppKit puts them back whenever it lays the title bar out again, so they are
/// placed again after each. Full screen keeps AppKit's own placement.
struct WindowChrome: NSViewRepresentable {
  static let inset: CGFloat = 20

  func makeNSView(context: Context) -> Probe { Probe() }
  func updateNSView(_ view: Probe, context: Context) {}

  static func apply(to window: NSWindow) {
    window.styleMask.insert(.fullSizeContentView)
    window.titleVisibility = .hidden
    window.titlebarAppearsTransparent = true
    window.titlebarSeparatorStyle = .none
    placeLights(in: window)
  }

  static func placeLights(in window: NSWindow) {
    guard !window.styleMask.contains(.fullScreen),
      let close = window.standardWindowButton(.closeButton),
      let mini = window.standardWindowButton(.miniaturizeButton),
      let zoom = window.standardWindowButton(.zoomButton),
      let titlebar = close.superview, let container = titlebar.superview
    else { return }
    var frame = container.frame
    frame.size.height = Head.band
    frame.origin.y = window.frame.height - Head.band
    if container.frame != frame { container.frame = frame }
    if titlebar.frame != container.bounds { titlebar.frame = container.bounds }
    let gap = mini.frame.minX - close.frame.minX
    for (index, button) in [close, mini, zoom].enumerated() {
      let origin = NSPoint(
        x: inset + CGFloat(index) * gap, y: ((Head.band - button.frame.height) / 2).rounded())
      if button.frame.origin != origin { button.setFrameOrigin(origin) }
    }
  }

  final class Probe: NSView {
    private var observers: [NSObjectProtocol] = []

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidMoveToWindow() {
      super.viewDidMoveToWindow()
      observers.forEach(NotificationCenter.default.removeObserver)
      observers = []
      guard let window else { return }
      WindowChrome.apply(to: window)
      let names: [Notification.Name] = [
        NSWindow.didResizeNotification, NSWindow.didEndLiveResizeNotification,
        NSWindow.didExitFullScreenNotification, NSWindow.didBecomeKeyNotification,
        NSWindow.didChangeScreenNotification,
      ]
      observers = names.map { name in
        NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) {
          [weak window] _ in
          MainActor.assumeIsolated { if let window { WindowChrome.placeLights(in: window) } }
        }
      }
      // SwiftUI sets the window up after the first pass; place them again once it has.
      DispatchQueue.main.async { [weak window] in
        if let window { WindowChrome.placeLights(in: window) }
      }
    }

    override func layout() {
      super.layout()
      if let window { WindowChrome.placeLights(in: window) }
    }

    deinit { observers.forEach(NotificationCenter.default.removeObserver) }
  }
}
