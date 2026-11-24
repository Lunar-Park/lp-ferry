import MetalKit
import SwiftUI

// Ferry's room and its glass, ported from the Lunar Park style kit
// (park/apple/Sources/ParkKit/Glass.swift) so the app stays standalone.
//
// The room is the ambient field (`FerryRoomShader`): navy washed with steel blue and a
// sea-slate swell, a wandering cool light and soft orbs, drifting slowly enough that you
// notice only that the room is alive. One Metal view draws it, once per frame, for the whole
// window.
//
// Glass is drawn in two places. Each `GlassSurface` reports where it is and draws only what
// sits on the glass: a smoke tint, the sheen, a 1px lit top edge and a shaded lower edge, so
// a panel reads as an object with thickness. The room does the rest: inside every reported
// pane it bends, frosts and fringes itself, and outside it drops the pane's shadow. One draw
// call however many panes, so the room costs next to nothing to keep alive.

enum Ambient {
  /// The room drifts slowly; thirty frames a second keeps the glass's edges fluid.
  static let framesPerSecond = 30
  /// `FERRY_STILL=1` holds the room still, for measuring and for anyone who wants it.
  static let still = ProcessInfo.processInfo.environment["FERRY_STILL"] == "1"
  /// A snapshot run (`FERRY_SNAPSHOT_DIR`) draws the room into an image a window capture sees.
  static let snapshot = ProcessInfo.processInfo.environment["FERRY_SNAPSHOT_DIR"] != nil
}

/// One pane of glass, as the room needs to know it.
struct GlassPane: Equatable, Sendable {
  /// Where nothing clips a pane.
  static let everywhere = CGRect(x: -100_000, y: -100_000, width: 200_000, height: 200_000)

  let rect: CGRect
  let radius: CGFloat
  /// The part of the window the pane can be seen in (a scroll view's frame).
  var clip: CGRect = Self.everywhere
}

struct GlassPanesKey: PreferenceKey {
  static let defaultValue: [GlassPane] = []
  static func reduce(value: inout [GlassPane], nextValue: () -> [GlassPane]) {
    value.append(contentsOf: nextValue())
  }
}

private struct GlassPanesEnvironmentKey: EnvironmentKey {
  static let defaultValue: [GlassPane] = []
}

private struct GlassClipKey: EnvironmentKey {
  static let defaultValue = GlassPane.everywhere
}

extension EnvironmentValues {
  /// The panes of glass on screen, for the room to bend under.
  var glassPanes: [GlassPane] {
    get { self[GlassPanesEnvironmentKey.self] }
    set { self[GlassPanesEnvironmentKey.self] = newValue }
  }
  /// The window rectangle the glass inside a scroll view is visible in.
  var glassClip: CGRect {
    get { self[GlassClipKey.self] }
    set { self[GlassClipKey.self] = newValue }
  }
}

/// The room behind every page. It drifts while the app is in front; the Metal view also
/// stops itself while its window is covered or minimised. A snapshot draws the same pass
/// into an image, so a capture of the window shows the real refraction under every pane.
struct Ground: View {
  @Environment(\.glassPanes) private var panes
  @Environment(\.accessibilityReduceMotion) private var reduceMotion
  @Environment(\.controlActiveState) private var active
  private let still: Bool
  private let snapshot: Bool

  init(still: Bool = Ambient.still, snapshot: Bool = Ambient.snapshot) {
    self.still = still
    self.snapshot = snapshot
  }

  var body: some View {
    Group {
      if snapshot {
        GeometryReader { geometry in
          if let image = RoomRenderer.snapshot(size: geometry.size, panes: panes) {
            Image(decorative: image, scale: 1).resizable()
          } else {
            Palette.ground
          }
        }
      } else {
        RoomView(panes: panes, running: active != .inactive && !still && !reduceMotion)
      }
    }
    .ignoresSafeArea()
    .allowsHitTesting(false)
  }
}

/// A window's root: hands the panes it hears about to the room.
struct AmbientRoom<Content: View>: View {
  @ViewBuilder var content: Content
  @State private var panes: [GlassPane] = []

  var body: some View {
    content
      .environment(\.glassPanes, panes)
      .onPreferenceChange(GlassPanesKey.self) { panes = $0 }
  }
}

/// A vertical scroll view whose glass stops bending the room where the scroll view ends.
struct GlassScroll<Content: View>: View {
  @ViewBuilder var content: Content

  /// The scroll view's rows of the window, full width: content scrolls out at the top and
  /// bottom, but a pane's shadow still reaches sideways into the gutter.
  static func band(_ frame: CGRect) -> CGRect {
    CGRect(x: GlassPane.everywhere.minX, y: frame.minY, width: GlassPane.everywhere.width, height: frame.height)
  }

  var body: some View {
    GeometryReader { box in
      ScrollView(.vertical) {
        content.overlayScrollers().environment(\.glassClip, Self.band(box.frame(in: .global)))
      }
    }
  }
}

/// What sits on a pane: how dark its smoke is, how bright its sheen.
struct GlassStyle: Sendable {
  var smoke: Double
  var sheen: Double

  static let panel = GlassStyle(smoke: 0.10, sheen: 1)
  static let control = GlassStyle(smoke: 0.0, sheen: 1.2)
}

/// A pane of glass in the room. It tells the room where it is (the room bends and frosts
/// itself there) and draws what sits on the glass: smoke, an optional tint, the sheen, and
/// the lit and shaded edges that give it thickness. Use it as a background.
struct GlassSurface: View {
  var radius: CGFloat = 20
  var style: GlassStyle = .panel
  var tint: Color? = nil
  @Environment(\.glassClip) private var clip

  var body: some View {
    let shape = RoundedRectangle(cornerRadius: radius, style: .continuous)
    ZStack {
      shape.fill(Color.black.opacity(style.smoke))
      if let tint {
        shape.fill(
          LinearGradient(
            colors: [tint.opacity(0.30), tint.opacity(0.08)],
            startPoint: .topLeading, endPoint: .bottomTrailing))
      }
      // The sheen: brighter where light enters at the top left.
      shape.fill(
        LinearGradient(
          stops: [
            .init(color: .white.opacity(0.060 * style.sheen), location: 0),
            .init(color: .white.opacity(0.010 * style.sheen), location: 0.45),
            .init(color: .white.opacity(0.026 * style.sheen), location: 1),
          ],
          startPoint: .topLeading, endPoint: .bottomTrailing))
      // Thickness: a hairline lit along the top and left that falls away below, the 1px
      // inset highlight on top, and a faint shade inside the lower edge.
      shape.strokeBorder(
        LinearGradient(
          stops: [
            .init(color: .white.opacity(0.24), location: 0),
            .init(color: .white.opacity(0.06), location: 0.5),
            .init(color: .white.opacity(0.11), location: 1),
          ],
          startPoint: .topLeading, endPoint: .bottomTrailing), lineWidth: 1)
      shape.inset(by: 1).strokeBorder(
        LinearGradient(
          stops: [
            .init(color: .white.opacity(0.36), location: 0),
            .init(color: .clear, location: min(0.5, 6 / max(radius * 3, 1))),
          ],
          startPoint: .top, endPoint: .bottom), lineWidth: 1)
      shape.inset(by: 1).strokeBorder(
        LinearGradient(
          stops: [.init(color: .clear, location: 0.82), .init(color: .black.opacity(0.24), location: 1)],
          startPoint: .top, endPoint: .bottom), lineWidth: 2
      )
      .blur(radius: 1.5)
      .clipShape(shape)
    }
    .allowsHitTesting(false)
    .background {
      GeometryReader { box in
        Color.clear.preference(
          key: GlassPanesKey.self,
          value: [GlassPane(rect: box.frame(in: .global), radius: radius, clip: clip)])
      }
    }
  }
}

// MARK: - The Metal view

struct RoomView: NSViewRepresentable {
  let panes: [GlassPane]
  let running: Bool

  func makeCoordinator() -> RoomRenderer? { RoomRenderer() }

  func makeNSView(context: Context) -> MTKView {
    let view = RoomMTKView(frame: .zero, device: context.coordinator?.device)
    view.colorPixelFormat = .bgra8Unorm
    view.framebufferOnly = true
    view.preferredFramesPerSecond = Ambient.framesPerSecond
    view.clearColor = MTLClearColor(red: 0.075, green: 0.11, blue: 0.165, alpha: 1)
    view.layer?.isOpaque = true
    (view.layer as? CAMetalLayer)?.colorspace = CGColorSpace(name: CGColorSpace.sRGB)
    view.delegate = context.coordinator
    return view
  }

  func updateNSView(_ view: MTKView, context: Context) {
    context.coordinator?.panes = panes
    (view as? RoomMTKView)?.wantsToRun = running
  }
}

/// The room's Metal view. It runs on a clock only while it is wanted and can be seen;
/// otherwise it draws on demand (a resize, a pane moving), and not at all while its window
/// is covered or minimised.
final class RoomMTKView: MTKView {
  var wantsToRun = true {
    didSet { if wantsToRun != oldValue { apply() } else if !wantsToRun { redraw() } }
  }
  private var observers: [NSObjectProtocol] = []

  override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    observers.forEach(NotificationCenter.default.removeObserver)
    observers = []
    if let window {
      for name in [
        NSWindow.didChangeOcclusionStateNotification, NSWindow.didMiniaturizeNotification,
        NSWindow.didDeminiaturizeNotification,
      ] {
        observers.append(
          NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) {
            [weak self] _ in
            MainActor.assumeIsolated { self?.apply() }
          })
      }
    }
    apply()
  }

  private var seen: Bool {
    guard let window else { return false }
    return window.occlusionState.contains(.visible) && !window.isMiniaturized
  }

  override func setFrameSize(_ newSize: NSSize) {
    super.setFrameSize(newSize)
    if !wantsToRun { redraw() }
  }

  private func apply() {
    let running = wantsToRun && seen
    enableSetNeedsDisplay = !running
    isPaused = !running
    if !running { redraw() }
  }

  private func redraw() {
    guard seen else { return }
    needsDisplay = true
  }

  deinit { observers.forEach(NotificationCenter.default.removeObserver) }
}

final class RoomRenderer: NSObject, MTKViewDelegate {
  let device: MTLDevice
  private let queue: MTLCommandQueue
  private let pipeline: MTLRenderPipelineState
  var panes: [GlassPane] = []

  private struct Pane {
    var rect: SIMD4<Float>
    var extra: SIMD4<Float>
    var clip: SIMD4<Float>
  }

  /// The most panes the room bends under; the list goes to the GPU inline (3 KB at most).
  static let paneLimit = 64

  private struct Compiled {
    let device: MTLDevice
    let queue: MTLCommandQueue
    let pipeline: MTLRenderPipelineState
  }

  /// Compiled once for the app: a sheet's room shares the window's.
  private static let compiled: Compiled? = {
    guard let device = MTLCreateSystemDefaultDevice(), let queue = device.makeCommandQueue(),
      let library = try? device.makeLibrary(source: FerryRoomShader.source, options: nil),
      let vertex = library.makeFunction(name: "roomVertex"),
      let fragment = library.makeFunction(name: "roomFragment")
    else { return nil }
    let descriptor = MTLRenderPipelineDescriptor()
    descriptor.vertexFunction = vertex
    descriptor.fragmentFunction = fragment
    descriptor.colorAttachments[0].pixelFormat = .bgra8Unorm
    guard let pipeline = try? device.makeRenderPipelineState(descriptor: descriptor) else {
      return nil
    }
    return Compiled(device: device, queue: queue, pipeline: pipeline)
  }()

  /// Nil when this machine has no Metal device or the shader would not compile.
  init?(shared: Void = ()) {
    guard let compiled = Self.compiled else { return nil }
    self.device = compiled.device
    self.queue = compiled.queue
    self.pipeline = compiled.pipeline
    super.init()
  }

  /// The six lights' centres at time `t` for a room of `size` points (see `FerryRoomShader`).
  static func lights(size: CGSize, time t: Double) -> [SIMD2<Float>] {
    let w = max(Double(size.width), 1)
    let h = max(Double(size.height), 1)
    return [
      SIMD2(Float(w * (0.08 + 0.05 * sin(t / 37))), Float(h * (0.02 + 0.06 * cos(t / 43)))),
      SIMD2(Float(w * (1.02 + 0.05 * cos(t / 41))), Float(h * (1.02 + 0.05 * sin(t / 31)))),
      SIMD2(Float(w * (0.52 + 0.18 * sin(t / 53))), Float(h * (0.40 + 0.14 * cos(t / 47)))),
      SIMD2(Float(w * (0.30 + 0.20 * sin(t / 61))), Float(h * (0.28 + 0.16 * cos(t / 71)))),
      SIMD2(Float(w * (0.74 + 0.16 * cos(t / 67))), Float(h * (0.62 + 0.18 * sin(t / 59)))),
      SIMD2(Float(w * (0.55 + 0.24 * cos(t / 83))), Float(h * (0.85 + 0.10 * sin(t / 73)))),
    ]
  }

  /// The room as an image, for a snapshot: the same pass, drawn into a texture.
  static func snapshot(size points: CGSize, panes: [GlassPane]) -> CGImage? {
    guard let renderer = RoomRenderer() else { return nil }
    renderer.panes = panes
    let width = max(1, Int(points.width))
    let height = max(1, Int(points.height))
    let descriptor = MTLTextureDescriptor.texture2DDescriptor(
      pixelFormat: .bgra8Unorm, width: width, height: height, mipmapped: false)
    descriptor.usage = [.renderTarget, .shaderRead]
    descriptor.storageMode = .shared
    guard let texture = renderer.device.makeTexture(descriptor: descriptor),
      let buffer = renderer.queue.makeCommandBuffer()
    else { return nil }
    let pass = MTLRenderPassDescriptor()
    pass.colorAttachments[0].texture = texture
    pass.colorAttachments[0].loadAction = .clear
    pass.colorAttachments[0].storeAction = .store
    guard let encoder = buffer.makeRenderCommandEncoder(descriptor: pass) else { return nil }
    renderer.encode(encoder, points: points, scale: 1, seconds: 1_000)
    encoder.endEncoding()
    buffer.commit()
    buffer.waitUntilCompleted()
    var bytes = [UInt8](repeating: 0, count: width * height * 4)
    texture.getBytes(
      &bytes, bytesPerRow: width * 4, from: MTLRegionMake2D(0, 0, width, height), mipmapLevel: 0)
    guard let provider = CGDataProvider(data: Data(bytes) as CFData),
      let space = CGColorSpace(name: CGColorSpace.sRGB)
    else { return nil }
    return CGImage(
      width: width, height: height, bitsPerComponent: 8, bitsPerPixel: 32, bytesPerRow: width * 4,
      space: space,
      bitmapInfo: CGBitmapInfo(
        rawValue: CGImageAlphaInfo.premultipliedFirst.rawValue
          | CGBitmapInfo.byteOrder32Little.rawValue),
      provider: provider, decode: nil, shouldInterpolate: false, intent: .defaultIntent)
  }

  private func encode(
    _ encoder: MTLRenderCommandEncoder, points: CGSize, scale: Float, seconds: Double
  ) {
    var frame = SIMD4<Float>(Float(points.width), Float(points.height), scale, Float(seconds))
    var lights = Self.lights(size: points, time: Double(Float(seconds)))
    func box(_ r: CGRect) -> SIMD4<Float> {
      SIMD4(Float(r.minX), Float(r.minY), Float(r.width), Float(r.height))
    }
    var list = panes.prefix(Self.paneLimit).map {
      Pane(rect: box($0.rect), extra: SIMD4(Float($0.radius), 0, 0, 0), clip: box($0.clip))
    }
    let count = Int32(list.count)
    if list.isEmpty { list = [Pane(rect: .zero, extra: .zero, clip: .zero)] }
    var counts = SIMD4<Int32>(count, 1, 0, 0)
    encoder.setRenderPipelineState(pipeline)
    encoder.setFragmentBytes(&frame, length: MemoryLayout<SIMD4<Float>>.stride, index: 0)
    encoder.setFragmentBytes(&counts, length: MemoryLayout<SIMD4<Int32>>.stride, index: 1)
    encoder.setFragmentBytes(&list, length: MemoryLayout<Pane>.stride * list.count, index: 2)
    encoder.setFragmentBytes(
      &lights, length: MemoryLayout<SIMD2<Float>>.stride * lights.count, index: 3)
    encoder.drawPrimitives(type: .triangle, vertexStart: 0, vertexCount: 3)
  }

  func mtkView(_ view: MTKView, drawableSizeWillChange size: CGSize) {}

  func draw(in view: MTKView) {
    guard let pass = view.currentRenderPassDescriptor, let drawable = view.currentDrawable,
      let buffer = queue.makeCommandBuffer(),
      let encoder = buffer.makeRenderCommandEncoder(descriptor: pass)
    else { return }
    let points = view.bounds.size
    let scale = points.width > 0 ? Float(view.drawableSize.width / points.width) : 1
    let seconds = Date().timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 100_000)
    encode(encoder, points: points, scale: scale, seconds: seconds)
    encoder.endEncoding()
    // While the window is being resized, hand the frame to Core Animation's own transaction
    // so the room and the panes above it move together instead of tearing.
    let resizing = view.inLiveResize
    (view.layer as? CAMetalLayer)?.presentsWithTransaction = resizing
    if resizing {
      buffer.commit()
      buffer.waitUntilScheduled()
      drawable.present()
      return
    }
    buffer.present(drawable)
    buffer.commit()
  }
}
