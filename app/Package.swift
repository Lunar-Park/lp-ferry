// swift-tools-version: 5.10
import PackageDescription

let package = Package(
  name: "Ferry", platforms: [.macOS(.v14)],
  products: [
    .executable(name: "Ferry", targets: ["Ferry"]),
    .library(name: "FerryCore", targets: ["FerryCore"]),
  ],
  targets: [
    .target(name: "FerryCore"),
    .executableTarget(name: "Ferry", dependencies: ["FerryCore"], exclude: ["Resources"]),
    .testTarget(name: "FerryCoreTests", dependencies: ["FerryCore"]),
  ])
