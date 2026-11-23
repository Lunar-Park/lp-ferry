import SwiftUI

extension Color {
  init(hex: UInt32) {
    self.init(
      .sRGB, red: Double(hex >> 16) / 255, green: Double((hex >> 8) & 255) / 255,
      blue: Double(hex & 255) / 255)
  }
}

// Ferry's R-7 surfaces and R-8 interaction tokens.
enum Palette {
  static let ground = Color(hex: 0x142035)
  static let wash = Color(hex: 0x1E3350)
  static let rail = Color(hex: 0x0A1A2F)
  static let plate = Color(hex: 0x1D2B47)
  static let top = Color(hex: 0x1E2C48)
  static let rim = Color(hex: 0x3D4F74)
  static let indicator = Color(hex: 0x6E7C90)
  static let rule1 = Color(hex: 0x26334B)
  static let rule2 = Color(hex: 0x3B4C68)
  static let rule3 = indicator
  static let bone = Color(hex: 0xF6F5F2)
  static let ink1 = Color(hex: 0xEDEBE6)
  static let ink2 = Color(hex: 0xDCE5EA)
  static let ink3 = Color(hex: 0xA6A7A7)
  static let ink4 = Color(hex: 0x919497)
  static let brand = Color(hex: 0x75A1C6)
  static let accent = Color(hex: 0x54A4E7)
  static let accentHover = Color(hex: 0x99B9D3)
  static let onAccent = Color(hex: 0x0F1622)
  static let ok = Color(hex: 0x69CC98)
  static let warn = Color(hex: 0xE7BB67)
  static let attention = Color(hex: 0xC391E0)
  static let bad = Color(hex: 0xFF9092)
  static let run = accent
}
