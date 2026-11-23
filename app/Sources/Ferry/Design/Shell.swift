import AppKit
import FerryCore
import SwiftUI

// The window's spatial shell, ported from the Lunar Park style kit
// (park/apple/Sources/ParkNative/Shell.swift): glass discs floating on the room down the
// traffic lights' axis, one open head band over every page, and the window's own discs in
// the top-right corner. There is no sidebar box: the rail is the room with discs on it.

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

/// The window's top band, one height on every page: the traffic lights, the page's title and
/// the window's discs all sit on its midline, as a Mac toolbar window's do.
enum Head {
  static let band: CGFloat = 64
  /// The height of every control in the band.
  static let control: CGFloat = 32
  /// Where a page's content starts.
  static var content: CGFloat { band }
}

/// Ferry's places, in the order the rail reads them.
enum Place: String, CaseIterable, Identifiable {
  case dashboard = "Dashboard"
  case transfer = "Transfer"
  case activity = "Activity"
  case machines = "Machines & Drives"
  case presets = "Presets & Destinations"

  var id: String { rawValue }
  /// The rail's word under the disc.
  var short: String {
    switch self {
    case .machines: "Machines"
    case .presets: "Presets"
    default: rawValue
    }
  }
  var glyph: LineIcon? {
    switch self {
    case .dashboard: nil  // the ship mark
    case .transfer: .transfer
    case .activity: .activity
    case .machines: .machines
    case .presets: .presets
    }
  }
}

/// What the shell and its pages share that the daemon does not own.
@MainActor @Observable final class Chrome {
  /// Bumped to put the cursor in Activity's search field.
  var searchRequest = 0
  var activityFilter = "All"
  var doctor = false
}

/// The rail: Ferry's places as glass discs, a rule between the work and the library, then
/// Ferry Doctor at the foot.
struct Rail: View {
  @Bindable var store: Store
  let chrome: Chrome

  var body: some View {
    VStack(spacing: Space.s4) {
      ForEach(Place.allCases) { place in
        if place == .machines {
          Rectangle().fill(Palette.rule2).frame(width: 40, height: 1).padding(.vertical, 2)
        }
        RailButton(
          glyph: place.glyph, label: place.short, current: store.section == place.rawValue,
          badge: place == .activity ? store.approvals.count : 0
        ) {
          store.section = place.rawValue
        }
      }
      Spacer(minLength: Space.s4)
      Button {
        chrome.doctor = true
      } label: {
        LineIcon.doctor.stroke(
          Palette.ink3, style: StrokeStyle(lineWidth: 1.4, lineCap: .round, lineJoin: .round)
        )
        .frame(width: 18, height: 18)
      }
      .buttonStyle(.park(.standard, disc: true))
      .help("Ferry Doctor")
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
/// place you are in. Dashboard carries the ship.
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
    .accessibilityLabel(label)
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

/// The page's first line: its title in the band, then whatever the page puts beside it. The
/// window's own discs are not part of it; `Floaters` draws them once for every page.
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
      // The corner the floating discs sit in.
      Color.clear.frame(width: Floaters.width + Space.s4, height: 1)
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

/// Search, approvals and this Mac's daemon, in the window's top-right corner over every page.
struct Floaters: View {
  static let disc: CGFloat = 36
  static let width: CGFloat = 3 * disc + 2 * Space.s2
  static let top: CGFloat = (Head.band - disc) / 2

  @Bindable var store: Store
  let chrome: Chrome
  @State private var status = false

  var body: some View {
    HStack(spacing: Space.s2) {
      FloatDisc(help: "Search activity (⌘F)") {
        store.section = Place.activity.rawValue
        chrome.searchRequest += 1
      } label: {
        icon(.search)
      }
      .keyboardShortcut("f")
      FloatDisc(
        help: store.approvals.isEmpty
          ? "Approvals" : "Approvals: \(store.approvals.count) waiting for you",
        lit: !store.approvals.isEmpty && store.section == Place.activity.rawValue
          && chrome.activityFilter == "Approvals"
      ) {
        chrome.activityFilter = "Approvals"
        store.section = Place.activity.rawValue
      } label: {
        icon(.tray)
      }
      .overlay(alignment: .topTrailing) {
        if !store.approvals.isEmpty { Badge(count: store.approvals.count).offset(x: 3, y: -3) }
      }
      FloatDisc(help: daemonHelp) {
        status.toggle()
      } label: {
        Text(String((store.status["name"].text.isEmpty ? "F" : store.status["name"].text).prefix(1)))
          .font(.inter(TypeScale.sm, .semibold)).foregroundStyle(Palette.ink1)
      }
      .overlay(alignment: .bottomTrailing) {
        Circle().fill(store.online ? Palette.ok : Palette.warn).frame(width: 9, height: 9)
          .overlay(Circle().strokeBorder(Palette.groundDeep, lineWidth: 2))
          .offset(x: 1, y: 1)
      }
      .popover(isPresented: $status, arrowEdge: .bottom) {
        DaemonCard(store: store) {
          status = false
          chrome.doctor = true
        }
      }
    }
  }

  private var daemonHelp: String {
    store.online
      ? "\(store.status["name"].text) · daemon online · protocol v\(store.status["protocol"].count)"
      : "Daemon offline · \(store.connectionNote)"
  }

  private func icon(_ glyph: LineIcon) -> some View {
    glyph.stroke(Palette.ink1, style: StrokeStyle(lineWidth: 1.5, lineCap: .round, lineJoin: .round))
      .frame(width: 17, height: 17)
  }
}

/// A floating disc of clear glass.
struct FloatDisc<Label: View>: View {
  let help: String
  var lit = false
  let action: () -> Void
  @ViewBuilder var label: Label
  @State private var hovering = false

  var body: some View {
    Button(action: action) {
      label
        .frame(width: Floaters.disc, height: Floaters.disc)
        .background {
          if lit {
            Circle().fill(Palette.sel.opacity(0.18))
          }
          GlassSurface(radius: Floaters.disc / 2, style: hovering ? GlassStyle(smoke: 0, sheen: 1.8) : .control)
        }
        .contentShape(Circle())
        .offset(y: hovering ? -1 : 0)
    }
    .buttonStyle(.plain)
    .animation(.spring(response: 0.25, dampingFraction: 0.7), value: hovering)
    .onHover { hovering = $0 }
    .help(help)
    .accessibilityLabel(help)
  }
}

/// The daemon at a glance, from the corner disc.
struct DaemonCard: View {
  let store: Store
  let doctor: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: Space.s3) {
      HStack(spacing: Space.s2) {
        Circle().fill(store.online ? Palette.ok : Palette.warn).frame(width: 8, height: 8)
        Text(store.online ? "Daemon online" : "Daemon offline").font(.inter(TypeScale.base, .semibold))
      }
      VStack(alignment: .leading, spacing: 5) {
        fact("Machine", store.fixture ? "Fixture session" : store.status["name"].text)
        fact("Version", store.status["version"].text)
        fact("Protocol", store.online ? "v\(store.status["protocol"].count)" : "—")
        if !store.online { fact("Note", store.connectionNote) }
      }
      Button("Run Ferry Doctor", action: doctor).buttonStyle(.park(.standard, .small))
    }
    .padding(Space.s5)
    .frame(width: 260, alignment: .leading)
    .foregroundStyle(Palette.ink1)
  }

  private func fact(_ name: String, _ value: String) -> some View {
    HStack {
      Text(name).foregroundStyle(Palette.ink3)
      Spacer()
      Text(value.isEmpty ? "—" : value).foregroundStyle(Palette.ink1).lineLimit(1)
    }
    .font(.inter(TypeScale.xs))
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
