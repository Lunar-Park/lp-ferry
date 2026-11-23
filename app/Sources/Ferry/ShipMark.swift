import SwiftUI

/// Vector outlines from assets/brand/ferry-logo-black.svg.
struct ShipMark: Shape {
  func path(in rect: CGRect) -> Path {
    var hull = Path()
    hull.move(to: CGPoint(x: 619.27, y: 474.19))
    hull.addLine(to: CGPoint(x: 604.66, y: 467.29))
    hull.addCurve(
      to: CGPoint(x: 498.37, y: 403.09), control1: CGPoint(x: 568.57, y: 445.04),
      control2: CGPoint(x: 545.29, y: 405.82))
    hull.addCurve(
      to: CGPoint(x: 11.17, y: 402.27), control1: CGPoint(x: 336.14, y: 401.13),
      control2: CGPoint(x: 173.54, y: 402.75))
    hull.addCurve(
      to: CGPoint(x: 12.76, y: 377.98), control1: CGPoint(x: -4.36, y: 399.82),
      control2: CGPoint(x: -3.55, y: 379.49))
    hull.addCurve(
      to: CGPoint(x: 99.62, y: 378.03), control1: CGPoint(x: 41.71, y: 377.80),
      control2: CGPoint(x: 70.67, y: 378.23))
    hull.addCurve(
      to: CGPoint(x: 495.28, y: 374.83), control1: CGPoint(x: 231.40, y: 377.08),
      control2: CGPoint(x: 363.47, y: 375.82))
    hull.addCurve(
      to: CGPoint(x: 598.15, y: 384.66), control1: CGPoint(x: 528.35, y: 374.58),
      control2: CGPoint(x: 567.50, y: 371.55))
    hull.addCurve(
      to: CGPoint(x: 676.13, y: 444.33), control1: CGPoint(x: 629.87, y: 398.23),
      control2: CGPoint(x: 646.72, y: 427.94))
    hull.addCurve(
      to: CGPoint(x: 740.04, y: 459.30), control1: CGPoint(x: 694.74, y: 454.70),
      control2: CGPoint(x: 719.07, y: 460.09))
    hull.addCurve(
      to: CGPoint(x: 788.56, y: 459.37), control1: CGPoint(x: 756.02, y: 458.70),
      control2: CGPoint(x: 772.55, y: 458.99))
    hull.addCurve(
      to: CGPoint(x: 875.02, y: 459.34), control1: CGPoint(x: 817.34, y: 460.06),
      control2: CGPoint(x: 846.24, y: 458.78))
    hull.addLine(to: CGPoint(x: 868.76, y: 469.10))
    hull.addCurve(
      to: CGPoint(x: 811.32, y: 497.69), control1: CGPoint(x: 854.31, y: 486.61),
      control2: CGPoint(x: 834.05, y: 496.49))
    hull.addCurve(
      to: CGPoint(x: 701.83, y: 496.83), control1: CGPoint(x: 775.15, y: 499.61),
      control2: CGPoint(x: 737.94, y: 496.98))
    hull.addCurve(
      to: CGPoint(x: 234.87, y: 496.88), control1: CGPoint(x: 546.18, y: 496.18),
      control2: CGPoint(x: 390.52, y: 497.38))
    hull.addCurve(
      to: CGPoint(x: 221.64, y: 478.92), control1: CGPoint(x: 227.39, y: 496.60),
      control2: CGPoint(x: 217.41, y: 486.07))
    hull.addCurve(
      to: CGPoint(x: 227.84, y: 474.18), control1: CGPoint(x: 222.47, y: 477.51),
      control2: CGPoint(x: 226.34, y: 474.18))
    hull.addLine(to: CGPoint(x: 619.28, y: 474.18))
    hull.addLine(to: CGPoint(x: 619.27, y: 474.19))
    hull.closeSubpath()
    hull.move(to: CGPoint(x: 541.06, y: 273.19))
    hull.addLine(to: CGPoint(x: 444.08, y: 278.66))
    hull.addLine(to: CGPoint(x: 444.08, y: 66.33))
    hull.addCurve(
      to: CGPoint(x: 446.04, y: 60.47), control1: CGPoint(x: 444.08, y: 66.01),
      control2: CGPoint(x: 445.70, y: 61.29))
    hull.addCurve(
      to: CGPoint(x: 459.69, y: 49.09), control1: CGPoint(x: 448.38, y: 54.88),
      control2: CGPoint(x: 453.81, y: 50.50))
    hull.addCurve(
      to: CGPoint(x: 530.49, y: 39.34), control1: CGPoint(x: 481.89, y: 43.78),
      control2: CGPoint(x: 507.91, y: 44.20))
    hull.addCurve(
      to: CGPoint(x: 536.42, y: 39.69), control1: CGPoint(x: 532.46, y: 39.36),
      control2: CGPoint(x: 534.50, y: 39.07))
    hull.addCurve(
      to: CGPoint(x: 541.07, y: 43.65), control1: CGPoint(x: 537.36, y: 40.00),
      control2: CGPoint(x: 541.07, y: 43.23))
    hull.addLine(to: CGPoint(x: 541.07, y: 273.19))
    hull.addLine(to: CGPoint(x: 541.06, y: 273.19))
    hull.closeSubpath()
    hull.move(to: CGPoint(x: 417.49, y: 280.23))
    hull.addLine(to: CGPoint(x: 338.50, y: 284.93))
    hull.addCurve(
      to: CGPoint(x: 339.32, y: 270.42), control1: CGPoint(x: 338.41, y: 279.97),
      control2: CGPoint(x: 339.56, y: 275.48))
    hull.addCurve(
      to: CGPoint(x: 338.47, y: 109.31), control1: CGPoint(x: 336.83, y: 216.96),
      control2: CGPoint(x: 339.50, y: 162.93))
    hull.addCurve(
      to: CGPoint(x: 353.95, y: 89.59), control1: CGPoint(x: 339.20, y: 99.93),
      control2: CGPoint(x: 344.71, y: 91.98))
    hull.addLine(to: CGPoint(x: 410.07, y: 80.73))
    hull.addCurve(
      to: CGPoint(x: 417.50, y: 86.66), control1: CGPoint(x: 412.80, y: 79.71),
      control2: CGPoint(x: 417.50, y: 84.67))
    hull.addLine(to: CGPoint(x: 417.50, y: 280.23))
    hull.addLine(to: CGPoint(x: 417.49, y: 280.23))
    hull.closeSubpath()
    hull.move(to: CGPoint(x: 252.47, y: 148.45))
    hull.addCurve(
      to: CGPoint(x: 263.67, y: 135.40), control1: CGPoint(x: 253.32, y: 143.49),
      control2: CGPoint(x: 258.71, y: 136.92))
    hull.addLine(to: CGPoint(x: 307.49, y: 126.38))
    hull.addCurve(
      to: CGPoint(x: 313.13, y: 131.58), control1: CGPoint(x: 310.49, y: 126.92),
      control2: CGPoint(x: 312.42, y: 128.59))
    hull.addCurve(
      to: CGPoint(x: 312.65, y: 251.64), control1: CGPoint(x: 313.41, y: 171.49),
      control2: CGPoint(x: 314.31, y: 211.85))
    hull.addCurve(
      to: CGPoint(x: 312.67, y: 270.44), control1: CGPoint(x: 312.89, y: 257.98),
      control2: CGPoint(x: 312.70, y: 264.34))
    hull.addCurve(
      to: CGPoint(x: 312.63, y: 286.43), control1: CGPoint(x: 312.65, y: 275.77),
      control2: CGPoint(x: 312.78, y: 281.11))
    hull.addLine(to: CGPoint(x: 252.47, y: 290.40))
    hull.addLine(to: CGPoint(x: 252.47, y: 148.45))
    hull.addLine(to: CGPoint(x: 252.47, y: 148.45))
    hull.closeSubpath()
    hull.move(to: CGPoint(x: 109.74, y: 450.72))
    hull.addCurve(
      to: CGPoint(x: 100.74, y: 444.86), control1: CGPoint(x: 107.06, y: 450.26),
      control2: CGPoint(x: 102.42, y: 447.09))
    hull.addCurve(
      to: CGPoint(x: 108.16, y: 427.24), control1: CGPoint(x: 94.85, y: 437.01),
      control2: CGPoint(x: 98.68, y: 428.58))
    hull.addLine(to: CGPoint(x: 416.36, y: 427.22))
    hull.addCurve(
      to: CGPoint(x: 452.69, y: 446.42), control1: CGPoint(x: 431.92, y: 427.58),
      control2: CGPoint(x: 443.40, y: 434.36))
    hull.addCurve(
      to: CGPoint(x: 453.08, y: 450.72), control1: CGPoint(x: 453.50, y: 447.47),
      control2: CGPoint(x: 455.26, y: 450.72))
    hull.addLine(to: CGPoint(x: 109.74, y: 450.72))
    hull.closeSubpath()
    hull.move(to: CGPoint(x: 1017.96, y: 200.67))
    hull.addCurve(
      to: CGPoint(x: 948.99, y: 208.34), control1: CGPoint(x: 995.62, y: 202.16),
      control2: CGPoint(x: 971.45, y: 205.77))
    hull.addCurve(
      to: CGPoint(x: 829.98, y: 223.07), control1: CGPoint(x: 909.29, y: 212.88),
      control2: CGPoint(x: 869.66, y: 218.31))
    hull.addCurve(
      to: CGPoint(x: 686.00, y: 253.49), control1: CGPoint(x: 779.52, y: 229.12),
      control2: CGPoint(x: 731.68, y: 228.68))
    hull.addCurve(
      to: CGPoint(x: 613.34, y: 291.88), control1: CGPoint(x: 661.80, y: 266.64),
      control2: CGPoint(x: 641.41, y: 288.06))
    hull.addCurve(
      to: CGPoint(x: 593.06, y: 292.72), control1: CGPoint(x: 606.60, y: 292.80),
      control2: CGPoint(x: 599.85, y: 292.34))
    hull.addCurve(
      to: CGPoint(x: 212.99, y: 312.30), control1: CGPoint(x: 466.56, y: 299.78),
      control2: CGPoint(x: 339.29, y: 303.56))
    hull.addCurve(
      to: CGPoint(x: 197.12, y: 319.89), control1: CGPoint(x: 206.46, y: 312.75),
      control2: CGPoint(x: 198.62, y: 310.88))
    hull.addCurve(
      to: CGPoint(x: 197.00, y: 342.34), control1: CGPoint(x: 196.38, y: 324.34),
      control2: CGPoint(x: 196.40, y: 337.77))
    hull.addCurve(
      to: CGPoint(x: 201.60, y: 349.47), control1: CGPoint(x: 197.34, y: 344.99),
      control2: CGPoint(x: 198.90, y: 348.60))
    hull.addCurve(
      to: CGPoint(x: 261.51, y: 349.85), control1: CGPoint(x: 221.48, y: 349.94),
      control2: CGPoint(x: 241.57, y: 350.02))
    hull.addCurve(
      to: CGPoint(x: 521.87, y: 346.65), control1: CGPoint(x: 348.19, y: 349.13),
      control2: CGPoint(x: 435.15, y: 347.40))
    hull.addCurve(
      to: CGPoint(x: 645.34, y: 370.29), control1: CGPoint(x: 566.62, y: 346.26),
      control2: CGPoint(x: 607.23, y: 343.13))
    hull.addCurve(
      to: CGPoint(x: 690.97, y: 409.90), control1: CGPoint(x: 661.86, y: 382.06),
      control2: CGPoint(x: 674.67, y: 398.14))
    hull.addCurve(
      to: CGPoint(x: 749.45, y: 431.19), control1: CGPoint(x: 708.39, y: 422.47),
      control2: CGPoint(x: 727.90, y: 429.63))
    hull.addLine(to: CGPoint(x: 892.61, y: 431.15))
    hull.addCurve(
      to: CGPoint(x: 901.94, y: 419.36), control1: CGPoint(x: 895.17, y: 430.71),
      control2: CGPoint(x: 900.24, y: 421.94))
    hull.addCurve(
      to: CGPoint(x: 1031.75, y: 216.79), control1: CGPoint(x: 946.09, y: 352.44),
      control2: CGPoint(x: 987.61, y: 283.72))
    hull.addCurve(
      to: CGPoint(x: 1017.95, y: 200.65), control1: CGPoint(x: 1035.92, y: 204.94),
      control2: CGPoint(x: 1029.39, y: 199.89))
    hull.addLine(to: CGPoint(x: 1017.96, y: 200.67))
    hull.closeSubpath()
    hull.move(to: CGPoint(x: 951.64, y: 260.27))
    hull.addCurve(
      to: CGPoint(x: 937.93, y: 282.53), control1: CGPoint(x: 951.37, y: 261.81),
      control2: CGPoint(x: 939.72, y: 280.14))
    hull.addCurve(
      to: CGPoint(x: 913.71, y: 286.47), control1: CGPoint(x: 933.24, y: 288.77),
      control2: CGPoint(x: 921.16, y: 286.21))
    hull.addCurve(
      to: CGPoint(x: 848.06, y: 288.87), control1: CGPoint(x: 891.92, y: 287.22),
      control2: CGPoint(x: 869.87, y: 288.45))
    hull.addCurve(
      to: CGPoint(x: 824.53, y: 288.87), control1: CGPoint(x: 840.76, y: 289.01),
      control2: CGPoint(x: 831.65, y: 289.42))
    hull.addCurve(
      to: CGPoint(x: 818.71, y: 282.23), control1: CGPoint(x: 820.31, y: 288.55),
      control2: CGPoint(x: 818.10, y: 286.78))
    hull.addCurve(
      to: CGPoint(x: 828.54, y: 266.21), control1: CGPoint(x: 818.93, y: 280.56),
      control2: CGPoint(x: 826.89, y: 268.55))
    hull.addCurve(
      to: CGPoint(x: 835.86, y: 259.46), control1: CGPoint(x: 830.47, y: 263.47),
      control2: CGPoint(x: 832.37, y: 260.27))
    hull.addCurve(
      to: CGPoint(x: 858.18, y: 257.54), control1: CGPoint(x: 842.84, y: 257.83),
      control2: CGPoint(x: 851.21, y: 259.19))
    hull.addLine(to: CGPoint(x: 933.28, y: 253.65))
    hull.addCurve(
      to: CGPoint(x: 935.83, y: 253.07), control1: CGPoint(x: 934.15, y: 253.74),
      control2: CGPoint(x: 935.10, y: 253.10))
    hull.addCurve(
      to: CGPoint(x: 951.63, y: 260.28), control1: CGPoint(x: 942.40, y: 252.81),
      control2: CGPoint(x: 953.40, y: 250.39))
    hull.addLine(to: CGPoint(x: 951.64, y: 260.27))
    hull.closeSubpath()
    hull.move(to: CGPoint(x: 788.81, y: 114.21))
    hull.addCurve(
      to: CGPoint(x: 753.78, y: 51.48), control1: CGPoint(x: 778.17, y: 92.82),
      control2: CGPoint(x: 766.18, y: 71.90))
    hull.addLine(to: CGPoint(x: 595.37, y: 0.29))
    hull.addCurve(
      to: CGPoint(x: 568.78, y: 17.40), control1: CGPoint(x: 584.08, y: -1.66),
      control2: CGPoint(x: 571.80, y: 6.54))
    hull.addCurve(
      to: CGPoint(x: 567.65, y: 22.53), control1: CGPoint(x: 568.58, y: 18.11),
      control2: CGPoint(x: 567.65, y: 22.27))
    hull.addLine(to: CGPoint(x: 567.65, y: 271.63))
    hull.addLine(to: CGPoint(x: 591.52, y: 270.86))
    hull.addCurve(
      to: CGPoint(x: 647.49, y: 243.14), control1: CGPoint(x: 613.49, y: 270.99),
      control2: CGPoint(x: 629.76, y: 254.15))
    hull.addCurve(
      to: CGPoint(x: 785.31, y: 203.43), control1: CGPoint(x: 690.49, y: 216.44),
      control2: CGPoint(x: 735.97, y: 210.61))
    hull.addCurve(
      to: CGPoint(x: 789.02, y: 197.76), control1: CGPoint(x: 788.38, y: 202.60),
      control2: CGPoint(x: 788.81, y: 200.59))
    hull.addCurve(
      to: CGPoint(x: 788.81, y: 114.22), control1: CGPoint(x: 791.08, y: 170.94),
      control2: CGPoint(x: 787.55, y: 141.33))
    hull.addLine(to: CGPoint(x: 788.81, y: 114.21))
    hull.closeSubpath()
    hull.move(to: CGPoint(x: 753.81, y: 150.03))
    hull.addCurve(
      to: CGPoint(x: 748.85, y: 154.85), control1: CGPoint(x: 753.48, y: 153.32),
      control2: CGPoint(x: 752.20, y: 154.65))
    hull.addLine(to: CGPoint(x: 683.41, y: 139.83))
    hull.addCurve(
      to: CGPoint(x: 681.02, y: 133.62), control1: CGPoint(x: 681.51, y: 138.41),
      control2: CGPoint(x: 681.19, y: 135.84))
    hull.addCurve(
      to: CGPoint(x: 681.02, y: 110.88), control1: CGPoint(x: 680.57, y: 127.77),
      control2: CGPoint(x: 680.42, y: 116.59))
    hull.addCurve(
      to: CGPoint(x: 682.79, y: 106.01), control1: CGPoint(x: 681.14, y: 109.71),
      control2: CGPoint(x: 682.07, y: 106.73))
    hull.addCurve(
      to: CGPoint(x: 685.41, y: 105.07), control1: CGPoint(x: 683.36, y: 105.43),
      control2: CGPoint(x: 684.60, y: 105.31))
    hull.addLine(to: CGPoint(x: 750.62, y: 121.11))
    hull.addLine(to: CGPoint(x: 753.78, y: 124.99))
    hull.addCurve(
      to: CGPoint(x: 753.81, y: 150.04), control1: CGPoint(x: 753.17, y: 133.02),
      control2: CGPoint(x: 754.61, y: 142.17))
    hull.addLine(to: CGPoint(x: 753.81, y: 150.03))
    hull.closeSubpath()
    let scale = min(rect.width / 1033.02, rect.height / 498.39)
    return hull.applying(
      CGAffineTransform(
        a: scale, b: 0, c: 0, d: scale, tx: rect.midX - 516.51 * scale,
        ty: rect.midY - 249.195 * scale))
  }
}
