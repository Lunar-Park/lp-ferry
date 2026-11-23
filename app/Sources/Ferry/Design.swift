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
      Palette.ground
      RadialGradient(
        colors: [Palette.wash, .clear], center: .topLeading, startRadius: 0, endRadius: 700)
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
    let shape = RoundedRectangle(cornerRadius: 11, style: .continuous)
    return background {
      shape.fill(
        LinearGradient(colors: [Palette.top, Palette.plate], startPoint: .top, endPoint: .bottom))
      shape.fill(
        LinearGradient(
          colors: [Palette.brand.opacity(0.08), .clear, .black.opacity(0.045)],
          startPoint: .topLeading, endPoint: .bottomTrailing))
      shape.strokeBorder(
        LinearGradient(
          colors: [Palette.rim, Palette.rim.opacity(0.68), Palette.rule1],
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
  var actionTitle: String?
  var action: (() -> Void)?
  @ViewBuilder var content: () -> Content
  var body: some View {
    VStack(alignment: .leading, spacing: 12) {
      HStack {
        Text(title).font(.inter(16, .semibold)).foregroundStyle(glow ?? Palette.bone)
        Spacer()
        if let actionTitle, let action {
          Button(actionTitle, action: action).font(.inter(12, .medium)).foregroundStyle(
            Palette.brand)
        }
      }
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
        .overlay(Capsule().strokeBorder(role == .primary ? Color.clear : Palette.indicator))
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
    let style: (Color, String) =
      switch word.lowercased() {
      case "succeeded", "verified", "available", "online", "completed", "clean", "passed":
        (Palette.ok, "checkmark.circle.fill")
      case "running", "verifying", "streaming", "copy": (Palette.run, "arrow.right.circle.fill")
      case "partial", "needs_attention": (Palette.attention, "exclamationmark.circle.fill")
      case "failed", "error", "modified", "unwritable": (Palette.bad, "xmark.octagon.fill")
      case "needs_review", "awaiting_approval", "needs_confirmation", "needs approval":
        (Palette.warn, "exclamationmark.triangle.fill")
      case "excluded", "exclude": (Palette.ink3, "minus.circle.fill")
      case "skip_identical": (Palette.brand, "equal.circle")
      case "renamed": (Palette.brand, "arrow.turn.down.right")
      default: (Palette.ink3, "circle")
      }
    Label(word.replacingOccurrences(of: "_", with: " ").capitalized, systemImage: style.1)
      .font(.inter(11, .medium)).lineLimit(1).fixedSize().foregroundStyle(style.0)
      .padding(.horizontal, 8).padding(.vertical, 4)
      .background(Capsule().fill(style.0.opacity(0.09)))
      .overlay(Capsule().strokeBorder(style.0.opacity(0.28)))
  }
}

struct ColumnLabel: View {
  let title: String
  init(_ title: String) { self.title = title }
  var body: some View {
    Text(title.uppercased()).font(.inter(10, .semibold)).tracking(1.3).foregroundStyle(Palette.ink3)
  }
}

struct FerryMenu<Items: View>: View {
  let title: String
  @ViewBuilder var items: () -> Items
  @State private var hovered = false
  var body: some View {
    Menu(title) { items() }
      .menuStyle(.borderlessButton).menuIndicator(.visible)
      .font(.inter(12, .medium)).fixedSize().padding(.horizontal, 10).frame(height: 29)
      .background(Capsule().fill(hovered ? Palette.rim : Palette.ground))
      .overlay(
        Capsule().strokeBorder(hovered ? Palette.brand : Palette.indicator).allowsHitTesting(false)
      )
      .onHover { hovered = $0 }
  }
}

struct Segments: View {
  let choices: [String]
  @Binding var selection: String
  @State private var hovered: String?
  var body: some View {
    HStack(spacing: 2) {
      ForEach(choices, id: \.self) { item in
        Button {
          selection = item
        } label: {
          Text(item).font(.inter(12, .medium)).fixedSize().padding(.horizontal, 9).padding(
            .vertical, 5
          )
          .background(
            Capsule().fill(
              selection == item ? Palette.rim : hovered == item ? Palette.rule1 : .clear))
        }.buttonStyle(.plain).onHover { hovered = $0 ? item : nil }.accessibilityAddTraits(
          selection == item ? .isSelected : [])
      }
    }.padding(3).background(Palette.rail, in: Capsule()).overlay(
      Capsule().strokeBorder(Palette.indicator))
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
      .monospacedDigit().buttonStyle(.plain).textFieldStyle(StudioField()).tint(Palette.brand)
  }
}

struct StudioField: TextFieldStyle {
  func _body(configuration: TextField<Self._Label>) -> some View {
    configuration.textFieldStyle(.plain).padding(.horizontal, 10).padding(.vertical, 7)
      .background(Palette.rail, in: RoundedRectangle(cornerRadius: 6))
      .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(Palette.indicator))
  }
}
