import SwiftUI

// The buttons, one system for every view, ported from the Lunar Park style kit
// (park/apple/Sources/ParkNative/Buttons.swift). A button is a control, not a cell: its plate
// is the room's glass at a control's size (`GlassSurface`, as the rail's discs are), light
// and small, and only on the few buttons that need one.
//
//   primary      the one action of a view: the glass tinted ferry blue, its edge lit blue,
//                its words ice blue (Build plan, Approve & start, Pair machine)
//   standard     clear glass: faint fill, lit top edge, the room's soft shadow (Plan
//                offload, Send, Close review)
//   quiet        words or an icon, no plate; a faint wash under the pointer (Export,
//                Dismiss, a row's small actions)
//   destructive  the glass tinted coral, its words coral (Cancel transfer)
//
// Shape: a pill, its radius half its height; an icon alone is a disc. Heights: 32 (`regular`)
// and 26 (`small`, inside rows and cards); words 14, medium (semibold on the primary). Pressed
// it sinks a little; under the pointer it lifts a point and brightens. Focused, a 1.5-point
// ring 3 points outside it in wake mist. Disabled, the plate fades and the words go ghost.
// Fields (`parkField`) are the same pill sunk into the glass instead of raised from it.

enum ButtonKind { case primary, standard, quiet, destructive }

enum ControlSize {
  case regular, small
  var height: CGFloat { self == .regular ? Head.control : 26 }
}

struct ParkButtonStyle: ButtonStyle {
  var kind: ButtonKind = .standard
  var size: ControlSize = .regular
  /// An icon alone: a disc as wide as it is tall.
  var disc = false
  /// A fixed width.
  var width: CGFloat? = nil

  func makeBody(configuration: Configuration) -> some View {
    ParkButtonBody(configuration: configuration, kind: kind, size: size, disc: disc, width: width)
  }
}

extension ButtonStyle where Self == ParkButtonStyle {
  static var primary: ParkButtonStyle { ParkButtonStyle(kind: .primary) }
  static var standard: ParkButtonStyle { ParkButtonStyle(kind: .standard) }
  static var quiet: ParkButtonStyle { ParkButtonStyle(kind: .quiet) }
  static var destructive: ParkButtonStyle { ParkButtonStyle(kind: .destructive) }
  static func park(
    _ kind: ButtonKind, _ size: ControlSize = .regular, disc: Bool = false, width: CGFloat? = nil
  ) -> ParkButtonStyle {
    ParkButtonStyle(kind: kind, size: size, disc: disc, width: width)
  }
}

private struct ParkButtonBody: View {
  let configuration: ButtonStyleConfiguration
  let kind: ButtonKind
  let size: ControlSize
  let disc: Bool
  let width: CGFloat?
  @Environment(\.isEnabled) private var enabled
  @Environment(\.isFocused) private var focused
  @State private var hovering = false

  private var lit: Bool { hovering && enabled }
  private var pressed: Bool { configuration.isPressed && enabled }

  private var ink: Color {
    guard enabled else { return Palette.ghost }
    switch kind {
    case .primary: return Palette.accentInk
    case .destructive: return Palette.bad
    case .standard: return lit ? Palette.ink1 : Palette.ink1.opacity(0.92)
    case .quiet: return lit ? Palette.ink1 : Palette.ink2
    }
  }

  private var tint: Color? {
    switch kind {
    case .primary: return Palette.accent
    case .destructive: return Palette.bad
    default: return nil
    }
  }

  var body: some View {
    let height = size.height
    configuration.label
      .font(.inter(size == .small ? 13 : TypeScale.sm, kind == .primary ? .semibold : .medium))
      .labelStyle(PlateLabel(disc: disc))
      .lineLimit(1)
      .fixedSize()
      .foregroundStyle(ink)
      .padding(.horizontal, disc || width != nil ? 0 : height / 2)
      .frame(width: disc ? height : width, height: height)
      .background { plate(height: height) }
      .overlay {
        if focused {
          Capsule().inset(by: -3).strokeBorder(Palette.focusRing, lineWidth: 1.5)
            .allowsHitTesting(false)
        }
      }
      .contentShape(Capsule())
      .scaleEffect(pressed ? 0.97 : 1)
      .offset(y: lit && !pressed && kind != .quiet ? -1 : 0)
      .animation(.spring(response: 0.25, dampingFraction: 0.7), value: hovering)
      .animation(.easeOut(duration: 0.08), value: configuration.isPressed)
      .onHover { hovering = $0 }
  }

  @ViewBuilder private func plate(height: CGFloat) -> some View {
    let shape = Capsule(style: .continuous)
    switch kind {
    case .quiet:
      shape.fill(Color.white.opacity(lit ? 0.06 : 0))
    default:
      ZStack {
        GlassSurface(
          radius: height / 2, style: lit ? GlassStyle(smoke: 0, sheen: 1.8) : .control,
          tint: enabled ? tint : nil)
        if let tint, enabled {
          // The tinted kinds' edge, lit in their colour along the top.
          shape.strokeBorder(
            LinearGradient(
              colors: [tint.opacity(lit ? 0.62 : 0.48), tint.opacity(0.10)],
              startPoint: .top, endPoint: .bottom), lineWidth: 1)
        }
        if pressed { shape.fill(Color.black.opacity(0.12)) }
      }
      .opacity(enabled ? 1 : 0.45)
    }
  }
}

/// A label on a plate: its icon and words a little closer than a list's; on a disc, the icon
/// alone (the words are its tooltip's and VoiceOver's).
private struct PlateLabel: LabelStyle {
  let disc: Bool
  func makeBody(configuration: Configuration) -> some View {
    if disc {
      configuration.icon
    } else {
      HStack(spacing: 6) {
        configuration.icon
        configuration.title
      }
    }
  }
}

extension View {
  /// A field sunk into the glass: the button's pill (radius 16, or a rounded box of the same
  /// radius when it grows to several lines), filled darker than the glass, shaded along the
  /// top and lit along the bottom, the reverse of a raised plate.
  func parkField(radius: CGFloat = Head.control / 2) -> some View {
    let shape = RoundedRectangle(cornerRadius: radius, style: .continuous)
    return
      self
      .background(shape.fill(Color.black.opacity(0.24)))
      .overlay(
        shape.strokeBorder(
          LinearGradient(
            colors: [Color.black.opacity(0.30), Color.white.opacity(0.10)],
            startPoint: .top, endPoint: .bottom), lineWidth: 1
        )
        .allowsHitTesting(false))
  }
}

/// Text fields as sunk pills: no system bezel, Inter, the field's own padding.
struct ParkFieldStyle: TextFieldStyle {
  var icon: String? = nil
  func _body(configuration: TextField<Self._Label>) -> some View {
    HStack(spacing: Space.s2) {
      if let icon {
        Image(systemName: icon).font(.system(size: 12, weight: .medium))
          .foregroundStyle(Palette.ink4)
      }
      configuration.textFieldStyle(.plain).font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink1)
    }
    .padding(.horizontal, 14)
    .frame(minHeight: Head.control)
    .parkField()
  }
}
