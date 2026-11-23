import CoreText
import SwiftUI

extension Color {
  public init(hex: UInt32, opacity: Double = 1) {
    self.init(
      .sRGB,
      red: Double((hex >> 16) & 0xFF) / 255,
      green: Double((hex >> 8) & 0xFF) / 255,
      blue: Double(hex & 0xFF) / 255,
      opacity: opacity)
  }

}

/// Park's tokens (`park/app/tokens.css`), one for one: mineral graphite, smoked silver,
/// warm ivory ink and one burnished amber for action. A view that writes a literal colour
/// is a bug; a new tone is an edit here.
public enum Palette {
  // The room
  public static let ground = Color(hex: 0x202225)
  public static let groundDeep = Color(hex: 0x15171A)
  public static let plate = Color(hex: 0x27292C)
  public static let plateHover = Color(hex: 0x303236)
  public static let plateRaised = Color(hex: 0x3B3E42)
  public static let well = Color(hex: 0x1D1F22)

  // Rules: a divider in a plate, the hairline between plates, a control's edge, under the pointer
  public static let rule1 = Color(hex: 0x303337)
  public static let rule2 = Color(hex: 0x3A3D41)
  public static let rule3 = Color(hex: 0x464A4F)
  public static let rule4 = Color(hex: 0x5B6066)

  // Ink: every text step clears 4.5:1 on the ground, a plate and a well
  public static let ink1 = Color(hex: 0xEEEEE8)
  public static let ink2 = Color(hex: 0xCED0CD)
  public static let ink3 = Color(hex: 0xB1B6B9)
  public static let ink4 = Color(hex: 0x9CA3AA)
  public static let ghost = Color(hex: 0x72787E)

  // The accent: the primary action and the brand mark, never a status or a selection
  public static let accent = Color(hex: 0xDFAA6E)
  public static let accentHover = Color(hex: 0xEDBA7E)
  public static let accentSoft = Color(hex: 0xDFAA6E, opacity: 0.09)
  public static let accentLine = Color(hex: 0xDFAA6E, opacity: 0.32)
  public static let onAccent = Color(hex: 0x241B11)

  // Selection is neutral
  public static let sel = Color(hex: 0xD7DBDC)
  public static let selSoft = Color(hex: 0xD7DBDC, opacity: 0.09)

  // Status: a glyph and a word first, colour third
  public static let ok = Color(hex: 0xB7BDC3)
  public static let warn = Color(hex: 0xE2BC70)
  public static let bad = Color(hex: 0xF07B74)
  public static let run = Color(hex: 0x80B9EF)
  public static let idle = Color(hex: 0x888C93)
  public static let warnSoft = Color(hex: 0x28241C)
  public static let badSoft = Color(hex: 0x2E1D1E)
  public static let runSoft = Color(hex: 0x1A2632)

  // Glass, for what floats: rail buttons, Search, Inbox, sheets
  public static let glassFill = Color.white.opacity(0.025)
  public static let glassHover = Color.white.opacity(0.045)
  public static let glassSelected = Color.white.opacity(0.08)
  public static let glassLine = Color.white.opacity(0.055)
  public static let glassEdge = Color.white.opacity(0.10)
  public static let glassCard = Color.white.opacity(0.018)
  public static let glassStrong = Color(hex: 0x2C2F33, opacity: 0.94)

}
