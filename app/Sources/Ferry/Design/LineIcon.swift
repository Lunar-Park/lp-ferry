import SwiftUI

/// The rail's line icons, drawn on a 24-point grid the way the Lunar Park kit draws Park's
/// (park/apple/Sources/ParkNative/LineIcon.swift). Stroke them at about 1.4 with round ends.
struct LineIcon: Shape {
  struct Dot: Hashable { let x, y, r: CGFloat }

  let drawing: String
  var dots: [Dot] = []

  /// Two crossings: a load going out above, one coming back below.
  static let transfer = LineIcon(drawing: "M4 8.5h14M14.5 5l3.5 3.5l-3.5 3.5M20 15.5h-14M9.5 12l-3.5 3.5l3.5 3.5")
  /// A pulse on the water line.
  static let activity = LineIcon(drawing: "M3 12.5h4l2.5-6l4 11l2.5-5h5")
  /// A drive on its dock, with its light.
  static let machines = LineIcon(
    drawing: "M4.5 6.5h15v7h-15zM4.5 13.5l-1 4h17l-1-4M7.5 10h5", dots: [Dot(x: 16.5, y: 10, r: 0.9)])
  /// Routing rules: three channels, each with its marker.
  static let presets = LineIcon(
    drawing: "M4 6.5h3M11 6.5h9M4 12h9.5M17.5 12h2.5M4 17.5h1M9 17.5h11",
    dots: [Dot(x: 9, y: 6.5, r: 2), Dot(x: 15.5, y: 12, r: 2), Dot(x: 7, y: 17.5, r: 2)])
  /// A lifebuoy: Ferry Doctor.
  static let doctor = LineIcon(
    drawing: "M12 4v4.2M12 15.8v4.2M4 12h4.2M15.8 12h4.2",
    dots: [])
  /// A magnifier.
  static let search = LineIcon(drawing: "M15.5 15.5l4.5 4.5", dots: [])
  /// An inbox tray: approvals waiting.
  static let tray = LineIcon(drawing: "M4 13.5l2.5-7.5h11l2.5 7.5v5h-16zM4 13.5h4.5l1.5 2.5h4l1.5-2.5h4.5")

  /// Rings the dots draw open rather than filled (the lifebuoy, the magnifier's lens).
  var rings: [Dot] {
    switch drawing {
    case Self.doctor.drawing: return [Dot(x: 12, y: 12, r: 8), Dot(x: 12, y: 12, r: 3.8)]
    case Self.search.drawing: return [Dot(x: 10.5, y: 10.5, r: 6)]
    default: return []
    }
  }

  func path(in rect: CGRect) -> Path {
    var path = Self.parse(drawing)
    for dot in dots + rings {
      path.addEllipse(in: CGRect(x: dot.x - dot.r, y: dot.y - dot.r, width: dot.r * 2, height: dot.r * 2))
    }
    let scale = min(rect.width, rect.height) / 24
    return path.applying(
      CGAffineTransform(translationX: rect.minX, y: rect.minY).scaledBy(x: scale, y: scale))
  }

  /// The subset of SVG path data the icons use: M L H V C Z, absolute or relative.
  static func parse(_ data: String) -> Path {
    var path = Path()
    var point = CGPoint.zero
    var start = CGPoint.zero
    for (command, values) in segments(data) {
      let relative = command.isLowercase
      func at(_ x: CGFloat, _ y: CGFloat) -> CGPoint {
        relative ? CGPoint(x: point.x + x, y: point.y + y) : CGPoint(x: x, y: y)
      }
      switch command.lowercased() {
      case "m":
        for (index, pair) in values.chunked(2).enumerated() {
          point = at(pair[0], pair[1])
          if index == 0 {
            path.move(to: point)
            start = point
          } else {
            path.addLine(to: point)
          }
        }
      case "l":
        for pair in values.chunked(2) {
          point = at(pair[0], pair[1])
          path.addLine(to: point)
        }
      case "h":
        for value in values {
          point = CGPoint(x: relative ? point.x + value : value, y: point.y)
          path.addLine(to: point)
        }
      case "v":
        for value in values {
          point = CGPoint(x: point.x, y: relative ? point.y + value : value)
          path.addLine(to: point)
        }
      case "c":
        for six in values.chunked(6) {
          let end = at(six[4], six[5])
          path.addCurve(to: end, control1: at(six[0], six[1]), control2: at(six[2], six[3]))
          point = end
        }
      case "z":
        path.closeSubpath()
        point = start
      default: break
      }
    }
    return path
  }

  private static func segments(_ data: String) -> [(Character, [CGFloat])] {
    var result: [(Character, [CGFloat])] = []
    var number = ""
    func flush() {
      if let value = Double(number), !result.isEmpty {
        result[result.count - 1].1.append(CGFloat(value))
      }
      number = ""
    }
    for character in data {
      if character.isLetter {
        flush()
        result.append((character, []))
      } else if character == "-" {
        flush()
        number = "-"
      } else if character == " " || character == "," {
        flush()
      } else {
        number.append(character)
      }
    }
    flush()
    return result
  }
}

extension Array {
  fileprivate func chunked(_ size: Int) -> [[Element]] {
    stride(from: 0, to: count - size + 1, by: size).map { Array(self[$0..<$0 + size]) }
  }
}
