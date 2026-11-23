import CoreText
import FerryCore
import SwiftUI

extension Font {
  static func inter(_ size: CGFloat, _ weight: Weight = .regular) -> Font {
    .custom("Inter", size: size).weight(weight).monospacedDigit()
  }
}
struct FerryRoom: View {
  var body: some View {
    ZStack {
      LinearGradient(
        colors: [Palette.ground, Palette.groundDeep], startPoint: .top, endPoint: .bottom)
      RadialGradient(
        colors: [.white.opacity(0.035), .clear], center: .top, startRadius: 0, endRadius: 700)
      Canvas { ctx, size in
        var grain = Path()
        for i in 0..<Int(size.width * size.height / 40) {
          let x = CGFloat((i &* 73243) % 65521) / 65521 * size.width
          let y = CGFloat(((i &* 14503) ^ (i >> 3)) % 65497) / 65497 * size.height
          grain.addRect(CGRect(x: x, y: y, width: 1, height: 1))
        }
        ctx.fill(grain, with: .color(.white.opacity(0.027)))
      }.drawingGroup()
    }.ignoresSafeArea()
  }
}
extension View {
  func paneGlass(glow: Color? = nil) -> some View {
    let shape = RoundedRectangle(cornerRadius: 15, style: .continuous)
    return background {
      shape.fill(.black.opacity(0.10))
      shape.fill(
        LinearGradient(
          colors: [.white.opacity(0.06), .white.opacity(0.01), .white.opacity(0.026)],
          startPoint: .topLeading, endPoint: .bottomTrailing))
      shape.strokeBorder(
        LinearGradient(
          colors: [.white.opacity(0.24), .white.opacity(0.06), .white.opacity(0.11)],
          startPoint: .topLeading, endPoint: .bottomTrailing))
      if let glow {
        shape.strokeBorder(glow.opacity(0.3)).shadow(color: glow.opacity(0.3), radius: 5)
      }
    }
  }
}
struct Panel<Content: View>: View {
  let title: String
  var glow: Color?
  @ViewBuilder var content: () -> Content
  var body: some View {
    VStack(alignment: .leading, spacing: 12) {
      Text(title.uppercased()).font(.inter(11, .semibold)).tracking(0.8).foregroundStyle(
        glow ?? Palette.ink4)
      content()
    }.padding(18).frame(maxWidth: .infinity, alignment: .leading).paneGlass(glow: glow)
  }
}
enum PillRole { case primary, secondary, destructive }
struct PillButton: View {
  let title: String
  var role: PillRole = .secondary
  var enabled = true
  let action: () -> Void
  @State private var hover = false
  var body: some View {
    Button(action: action) {
      Text(title).font(.inter(13, .semibold)).fixedSize()
        .padding(.horizontal, 15).frame(height: 31)
        .foregroundStyle(
          !enabled
            ? Palette.ink4
            : role == .primary
              ? Palette.onAccent : role == .destructive ? Palette.bad : Palette.ink1
        )
        .background(
          Capsule().fill(
            !enabled
              ? Color.white.opacity(0.05)
              : role == .primary
                ? (hover ? Palette.accentHover : Palette.accent)
                : (role == .destructive ? Palette.bad : Color.white).opacity(hover ? 0.16 : 0.08))
        )
        .overlay(Capsule().strokeBorder(.white.opacity(0.06)))
    }.buttonStyle(.plain).onHover { hover = $0 }.disabled(!enabled)
  }
}
struct Detail: View {
  let text: String
  var body: some View {
    Text(text).font(.system(size: 12, design: .monospaced)).foregroundStyle(Palette.ink3)
      .textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
  }
}
struct StateLabel: View {
  let word: String
  var body: some View {
    let good = ["available", "online", "completed", "clean", "passed"].contains(word)
    let run = ["running", "verifying", "streaming"].contains(word)
    let bad = ["failed", "error", "modified", "unwritable"].contains(word)
    Label(
      word.replacingOccurrences(of: "_", with: " ").capitalized,
      systemImage: good
        ? "checkmark.circle"
        : run ? "arrow.triangle.2.circlepath" : bad ? "xmark.octagon" : "exclamationmark.triangle"
    )
    .font(.inter(12, .medium)).foregroundStyle(
      good ? Palette.ok : run ? Palette.run : bad ? Palette.bad : Palette.warn)
  }
}
func copyText(_ text: String) {
  NSPasteboard.general.clearContents()
  NSPasteboard.general.setString(text, forType: .string)
}
@MainActor func pickFolders(multiple: Bool = true) -> [URL] {
  let panel = NSOpenPanel()
  panel.canChooseDirectories = true
  panel.canChooseFiles = false
  panel.allowsMultipleSelection = multiple
  return panel.runModal() == .OK ? panel.urls : []
}
struct SheetPage<Content: View>: View {
  let title: String
  @ViewBuilder var content: () -> Content
  @Environment(\.dismiss) private var dismiss
  var body: some View {
    VStack(alignment: .leading, spacing: 18) {
      HStack {
        Text(title).font(.inter(22, .semibold))
        Spacer()
        PillButton(title: "Done") { dismiss() }
      }
      ScrollView {
        VStack(alignment: .leading, spacing: 14) { content() }.frame(
          maxWidth: .infinity, alignment: .leading)
      }
    }.padding(24).frame(width: 550, height: 520).background(FerryRoom()).preferredColorScheme(.dark)
      .monospacedDigit()
  }
}
