import AppKit
import FerryCore
import SwiftUI

// The pieces every page is built from, in the kit's grammar: open lists on the room split by
// hairlines, glass only for what is raised (a hero card, an approval, an inspector, a
// sheet), and shape-first status.

/// A section's heading on the open room: 22 semibold, its count beside it, an optional way
/// on at the trailing edge.
struct SectionTitle<Trailing: View>: View {
  let title: String
  var count: Int? = nil
  @ViewBuilder var trailing: Trailing

  var body: some View {
    HStack(alignment: .firstTextBaseline, spacing: Space.s3) {
      Text(title).font(.inter(TypeScale.lg, .semibold)).tracking(-0.3).foregroundStyle(Palette.bone)
      if let count {
        Text("\(count)").font(.inter(TypeScale.lg, .regular)).foregroundStyle(Palette.ink3)
      }
      Spacer(minLength: Space.s3)
      trailing
    }
  }
}

extension SectionTitle where Trailing == EmptyView {
  init(_ title: String, count: Int? = nil) {
    self.init(title: title, count: count, trailing: { EmptyView() })
  }
}

/// A smaller heading inside a glass card.
struct CardTitle: View {
  let text: String
  init(_ text: String) { self.text = text }
  var body: some View {
    Text(text).font(.inter(TypeScale.md, .semibold)).foregroundStyle(Palette.bone)
  }
}

/// A quiet label over a value or a control.
struct FieldLabel: View {
  let text: String
  init(_ text: String) { self.text = text }
  var body: some View {
    Text(text).font(.inter(TypeScale.xs, .medium)).foregroundStyle(Palette.ink3)
  }
}

/// The hairline between rows on the open room.
struct Hairline: View {
  var color = Palette.rule1
  var body: some View { Rectangle().fill(color).frame(height: 1) }
}

/// An inline link with an arrow, in ferry blue (Park's "Resume ↗", "Compare options →").
struct ArrowLink: View {
  let title: String
  var symbol = "arrow.right"
  let action: () -> Void
  @State private var hovering = false

  var body: some View {
    Button(action: action) {
      HStack(spacing: 6) {
        Text(title)
        Image(systemName: symbol).font(.system(size: 11, weight: .semibold))
          .offset(x: hovering ? 2 : 0)
      }
      .font(.inter(TypeScale.sm, .semibold))
      .foregroundStyle(hovering ? Palette.accentInk : Palette.accent)
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
    .animation(.spring(response: 0.25, dampingFraction: 0.7), value: hovering)
    .onHover { hovering = $0 }
  }
}

/// A page's sub-places as words over a hairline, the chosen one lit with a bar beneath it
/// (Park's Desk: Everything, Relay, Oak Street).
struct UnderlineTabs: View {
  let choices: [String]
  @Binding var selection: String
  var counts: [String: Int] = [:]
  @State private var hovered: String?
  @Namespace private var bar

  var body: some View {
    HStack(spacing: Space.s7) {
      ForEach(choices, id: \.self) { item in
        let on = selection == item
        Button {
          withAnimation(.spring(response: 0.3, dampingFraction: 0.8)) { selection = item }
        } label: {
          HStack(spacing: 6) {
            Text(item)
            if let count = counts[item], count > 0 {
              Text("\(count)").foregroundStyle(item == "Approvals" ? Palette.warn : Palette.ink3)
            }
          }
          .font(.inter(TypeScale.base, on ? .semibold : .regular))
          .foregroundStyle(on ? Palette.bone : hovered == item ? Palette.ink1 : Palette.ink3)
          .padding(.bottom, 11)
          .overlay(alignment: .bottom) {
            if on {
              Capsule().fill(Palette.bone).frame(height: 2).matchedGeometryEffect(id: "bar", in: bar)
            }
          }
          .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovered = $0 ? item : nil }
        .accessibilityAddTraits(on ? .isSelected : [])
      }
    }
    .fixedSize()
  }
}

/// A small choice of two or three: a sunk track with the chosen one raised in glass
/// (Park's Inbox: Needs you, Resolved).
struct Segments: View {
  let choices: [String]
  @Binding var selection: String
  @State private var hovered: String?

  var body: some View {
    HStack(spacing: 2) {
      ForEach(choices, id: \.self) { item in
        let on = selection == item
        Button {
          selection = item
        } label: {
          Text(item).font(.inter(13, on ? .semibold : .medium)).fixedSize()
            .foregroundStyle(on ? Palette.bone : hovered == item ? Palette.ink1 : Palette.ink3)
            .padding(.horizontal, 14).frame(height: Head.control - 6)
            .background {
              if on { GlassSurface(radius: (Head.control - 6) / 2, style: GlassStyle(smoke: 0, sheen: 1.8)) }
            }
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .onHover { hovered = $0 ? item : nil }
        .accessibilityAddTraits(on ? .isSelected : [])
      }
    }
    .padding(3)
    .parkField()
  }
}

/// A menu as a glass pill: its value and a small chevron. Never the grey system pop-up.
struct GlassMenu<Items: View>: View {
  let title: String
  var icon: String? = nil
  var size: ControlSize = .regular
  @ViewBuilder var items: Items
  @State private var hovering = false

  var body: some View {
    Menu {
      items
    } label: {
      HStack(spacing: 7) {
        if let icon { Image(systemName: icon).font(.system(size: 12, weight: .medium)) }
        Text(title).lineLimit(1)
      }
    }
    .menuStyle(.borderlessButton)
    .menuIndicator(.hidden)
    .fixedSize()
    .font(.inter(size == .small ? 13 : TypeScale.sm, .medium))
    .tint(Palette.ink1)
    .foregroundStyle(Palette.ink1)
    .padding(.leading, size.height / 2)
    .padding(.trailing, size.height / 2 + 14)
    .frame(height: size.height)
    .overlay(alignment: .trailing) {
      Image(systemName: "chevron.down").font(.system(size: 9, weight: .bold))
        .foregroundStyle(Palette.ink3).padding(.trailing, size.height / 2 - 2)
        .allowsHitTesting(false)
    }
    .background {
      GlassSurface(
        radius: size.height / 2, style: hovering ? GlassStyle(smoke: 0, sheen: 1.8) : .control)
    }
    .contentShape(Capsule())
    .offset(y: hovering ? -1 : 0)
    .animation(.spring(response: 0.25, dampingFraction: 0.7), value: hovering)
    .onHover { hovering = $0 }
  }
}

/// A row's overflow menu: a quiet disc with an ellipsis.
struct MoreMenu<Items: View>: View {
  var help = "More"
  @ViewBuilder var items: Items
  @State private var hovering = false

  var body: some View {
    Menu {
      items
    } label: {
      Image(systemName: "ellipsis").font(.system(size: 13, weight: .semibold))
    }
    .menuStyle(.borderlessButton)
    .menuIndicator(.hidden)
    .tint(Palette.ink2)
    .fixedSize()
    .frame(width: 26, height: 26)
    .background(Circle().fill(Color.white.opacity(hovering ? 0.08 : 0)))
    .contentShape(Circle())
    .onHover { hovering = $0 }
    .help(help)
  }
}

/// Status, shape first: a glyph and a word on a faint wash of its colour.
struct StatusPill: View {
  let word: String

  static func style(_ word: String) -> (Color, String) {
    switch word.lowercased() {
    case "succeeded", "verified", "available", "online", "completed", "clean", "passed":
      (Palette.ok, "checkmark")
    case "running", "verifying", "streaming", "copy", "queued": (Palette.run, "arrow.right")
    case "partial", "needs_attention": (Palette.attention, "exclamationmark")
    case "failed", "error", "modified", "unwritable": (Palette.bad, "xmark")
    case "needs_review", "awaiting_approval", "needs_confirmation", "needs approval":
      (Palette.warn, "hand.raised")
    case "excluded", "exclude", "canceled": (Palette.ink3, "minus")
    case "skip_identical": (Palette.ink2, "equal")
    case "renamed": (Palette.ink2, "arrow.turn.down.right")
    case "remote": (Palette.accent, "network")
    default: (Palette.ink3, "circle")
    }
  }

  var body: some View {
    let (color, symbol) = Self.style(word)
    HStack(spacing: 5) {
      Image(systemName: symbol).font(.system(size: 9, weight: .bold))
      Text(word.replacingOccurrences(of: "_", with: " ").capitalized)
    }
    .font(.inter(TypeScale.xs, .semibold)).lineLimit(1).fixedSize()
    .foregroundStyle(color)
    .padding(.horizontal, 9).frame(height: 22)
    .background(Capsule().fill(color.opacity(0.13)))
  }
}

/// How bytes reach a place: a quiet tag with its own small glyph.
struct TransportBadge: View {
  let text: String

  var body: some View {
    let word = text.isEmpty ? "Route unknown" : text
    let symbol =
      word.contains("card")
      ? "sdcard" : word.contains("Tailscale") || word.contains("WAN")
        ? "globe" : word.contains("LAN") ? "cable.connector" : word.contains("Network") ? "network" : "bolt"
    HStack(spacing: 5) {
      Image(systemName: symbol).font(.system(size: 10, weight: .medium)).foregroundStyle(Palette.accent)
      Text(word).foregroundStyle(Palette.ink2)
    }
    .font(.inter(TypeScale.xs, .medium)).lineLimit(1).fixedSize()
    .padding(.horizontal, 9).frame(height: 22)
    .background(Capsule().fill(Color.white.opacity(0.06)))
  }
}

/// A thing's icon on the open room: a small glass disc with a line glyph in ferry blue.
struct IconDisc: View {
  let symbol: String
  var size: CGFloat = 36

  var body: some View {
    Image(systemName: symbol).font(.system(size: size * 0.4, weight: .regular))
      .foregroundStyle(Palette.accent)
      .frame(width: size, height: size)
      .background(GlassSurface(radius: size / 2, style: .control))
  }
}

/// A thin frosted bar: how far along, lit from the top.
struct Meter: View {
  let fraction: Double
  var color = Palette.run
  var height: CGFloat = 5

  var body: some View {
    GeometryReader { box in
      ZStack(alignment: .leading) {
        Capsule().fill(Color.black.opacity(0.28))
          .overlay(Capsule().strokeBorder(Color.white.opacity(0.06), lineWidth: 0.5))
        Capsule(style: .continuous)
          .fill(LinearGradient(colors: [color, color.opacity(0.62)], startPoint: .top, endPoint: .bottom))
          .overlay(
            Capsule(style: .continuous).strokeBorder(
              LinearGradient(colors: [.white.opacity(0.45), .clear], startPoint: .top, endPoint: .center),
              lineWidth: 0.75)
          )
          .frame(width: max(height, box.size.width * max(0, min(1, fraction))))
          .shadow(color: color.opacity(0.45), radius: 4)
      }
    }
    .frame(height: height)
  }
}

/// Paths, fingerprints and digests: monospaced, selectable, quiet.
struct Detail: View {
  let text: String
  var color = Palette.ink3
  var body: some View {
    Text(text).font(.mono()).foregroundStyle(color)
      .textSelection(.enabled).lineLimit(1).truncationMode(.middle)
  }
}

/// A fold: a quiet heading that opens the part below it.
struct Fold<Content: View>: View {
  let title: String
  @ViewBuilder var content: Content
  @State private var open = false

  var body: some View {
    VStack(alignment: .leading, spacing: Space.s3) {
      Button {
        withAnimation(.spring(response: 0.3, dampingFraction: 0.85)) { open.toggle() }
      } label: {
        HStack(spacing: 7) {
          Image(systemName: "chevron.right").font(.system(size: 9, weight: .bold))
            .rotationEffect(.degrees(open ? 90 : 0))
          Text(title).font(.inter(13, .medium))
        }
        .foregroundStyle(Palette.ink2)
        .contentShape(Rectangle())
      }
      .buttonStyle(.plain)
      if open { content }
    }
  }
}

/// Nothing here yet, said quietly.
struct Quiet: View {
  let text: String
  init(_ text: String) { self.text = text }
  var body: some View {
    Text(text).font(.inter(TypeScale.sm)).foregroundStyle(Palette.ink3).padding(.vertical, 6)
  }
}

/// A sheet: its own room, a title, and Done.
struct SheetPage<Content: View>: View {
  let title: String
  var width: CGFloat = 560
  var height: CGFloat = 540
  @ViewBuilder var content: Content
  @Environment(\.dismiss) private var dismiss

  var body: some View {
    AmbientRoom {
      ZStack {
        Ground()
        VStack(alignment: .leading, spacing: Space.s5) {
          HStack {
            Text(title).font(.inter(TypeScale.lg, .semibold)).foregroundStyle(Palette.bone)
            Spacer()
            Button("Done") { dismiss() }.buttonStyle(.standard).keyboardShortcut(.cancelAction)
          }
          GlassScroll {
            VStack(alignment: .leading, spacing: Space.s4) { content }
              .frame(maxWidth: .infinity, alignment: .leading)
              .padding(.vertical, 4)
          }
        }
        .padding(Space.s6)
      }
    }
    .frame(width: width, height: height)
    .preferredColorScheme(.dark)
    .foregroundStyle(Palette.ink1)
    .font(.inter(TypeScale.sm))
    .tint(Palette.accent)
    .textFieldStyle(ParkFieldStyle())
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

func display(_ value: String) -> String {
  value.replacingOccurrences(of: "_", with: " ").capitalized
}
