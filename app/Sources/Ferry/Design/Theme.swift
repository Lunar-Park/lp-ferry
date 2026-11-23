import SwiftUI

// Ferry's tokens, laid out the way the Lunar Park style kit lays out Park's
// (park/apple/Sources/ParkKit/Theme.swift): the room, rules, ink, one accent for action, a
// neutral selection, and status colours that follow a glyph and a word. Park's graphite and
// amber become Ferry's maritime navy and ferry blue (docs/BRAND-STYLE-GUIDE.md at tag
// legacy-python-v0.3). A view that writes a literal colour is a bug; a new tone is an edit here.

extension Color {
  init(hex: UInt32, opacity: Double = 1) {
    self.init(
      .sRGB, red: Double((hex >> 16) & 0xFF) / 255, green: Double((hex >> 8) & 0xFF) / 255,
      blue: Double(hex & 0xFF) / 255, opacity: opacity)
  }
}

enum Palette {
  // The room: deep graphite-navy, matched to the shader's base (`FerryRoomShader`).
  static let ground = Color(hex: 0x131C2A)
  static let groundDeep = Color(hex: 0x0C121C)

  // Rules: a hairline between rows, the hairline under a head, a control's edge.
  static let rule1 = Color.white.opacity(0.07)
  static let rule2 = Color.white.opacity(0.10)
  static let rule3 = Color.white.opacity(0.16)

  // Ink: bone for titles and the mark, body for reading, then three quieter steps. Every
  // text step clears 4.5:1 on the room and on the glass.
  static let bone = Color(hex: 0xF6F5F2)
  static let ink1 = Color(hex: 0xEDEBE6)
  static let ink2 = Color(hex: 0xCAD3DB)
  static let ink3 = Color(hex: 0xA3AFBB)
  static let ink4 = Color(hex: 0x8B98A5)
  static let ghost = Color(hex: 0x667381)

  // The accent: ferry blue, for the primary action and the brand mark, never a status.
  static let accent = Color(hex: 0x75A1C6)
  /// Words on accent-tinted glass: ice blue, a step lighter so they read on the tint.
  static let accentInk = Color(hex: 0xC4E0F6)
  /// The ferry blue of an icon sitting on the mist current-place disc.
  static let accentDeep = Color(hex: 0x2D5A83)
  /// The ship's wake and cargo on the mist disc, a step lighter than its hull.
  static let accentMid = Color(hex: 0x5C8FBA)
  /// Pale wake mist: highlights and the keyboard focus ring.
  static let mist = Color(hex: 0x9EC5E6)
  static let focusRing = Color(hex: 0x9EC5E6, opacity: 0.78)

  // Selection is neutral: the lit disc of the place you are in.
  static let sel = Color(hex: 0xDCE5EA)

  // Status: a glyph and a word first, colour third.
  static let ok = Color(hex: 0x86CFA8)
  static let warn = Color(hex: 0xE7BB67)
  static let bad = Color(hex: 0xF2877F)
  static let run = Color(hex: 0x9EC5E6)
  static let attention = Color(hex: 0xC6A0E2)
  static let idle = Color(hex: 0x8B98A5)
}

/// Inter at Park's scale. Digits are always tabular, so telemetry never jitters.
enum TypeScale {
  static let xs: CGFloat = 12, sm: CGFloat = 14, base: CGFloat = 15, md: CGFloat = 17
  static let lg: CGFloat = 22, xl: CGFloat = 30
}

extension Font {
  static func inter(_ size: CGFloat, _ weight: Weight = .regular) -> Font {
    .custom("Inter", size: size).weight(weight).monospacedDigit()
  }
  /// Paths, fingerprints and digests.
  static func mono(_ size: CGFloat = 11.5, _ weight: Weight = .regular) -> Font {
    .system(size: size, weight: weight, design: .monospaced)
  }
}

enum Space {
  static let s1: CGFloat = 4, s2: CGFloat = 8, s3: CGFloat = 12, s4: CGFloat = 16
  static let s5: CGFloat = 20, s6: CGFloat = 24, s7: CGFloat = 32, s8: CGFloat = 48
}
