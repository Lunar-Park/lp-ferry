import SwiftUI

// The buttons, one system for every view, after the Lunar Park style kit
// (park/apple/Sources/ParkNative/Buttons.swift). Ferry spends the room's glass sparingly:
// only the one primary action of a screen is a glass plate; the rest are flat.
//
//   primary      the one action of a screen, and the only button made of the room's glass:
//                tinted ferry blue, its edge lit blue, its words ice blue (Review transfer,
//                Start verified copy, Verify on disk)
//   standard     a flat plate: a soft fill and a faint lit top edge (Pair machine, Edit)
//   quiet        words or an icon, no plate; a faint wash under the pointer (Offload,
//                Check speeds, a row's small actions)
//   destructive  a flat coral plate, its words coral (Cancel)
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
    case .primary:
      // The one action of a screen is the one button made of the room's glass.
      ZStack {
        GlassSurface(
          radius: height / 2, style: lit ? GlassStyle(smoke: 0, sheen: 1.8) : .control,
          tint: enabled ? Palette.accent : nil)
        if enabled {
          shape.strokeBorder(
            LinearGradient(
              colors: [Palette.accent.opacity(lit ? 0.70 : 0.55), Palette.accent.opacity(0.12)],
              startPoint: .top, endPoint: .bottom), lineWidth: 1)
        }
        if pressed { shape.fill(Color.black.opacity(0.12)) }
      }
      .opacity(enabled ? 1 : 0.45)
    case .standard, .destructive:
      // Everything else is a flat, crisp plate: a soft fill and a faint lit top edge, so it
      // never competes with the primary.
      let base = kind == .destructive ? Palette.bad : Color.white
      let fill = kind == .destructive ? (lit ? 0.20 : 0.13) : (lit ? 0.12 : 0.075)
      ZStack {
        shape.fill(base.opacity(pressed ? fill * 0.7 : fill))
        shape.strokeBorder(
          LinearGradient(
            colors: [Color.white.opacity(lit ? 0.14 : 0.09), Color.white.opacity(0.0)],
            startPoint: .top, endPoint: .center), lineWidth: 1)
      }
      .opacity(enabled ? 1 : 0.5)
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
