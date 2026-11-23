import Foundation

/// Preserve 64-bit ledger values and additive daemon response fields.
package enum JSON: Codable, Hashable, Sendable, ExpressibleByStringLiteral,
  ExpressibleByIntegerLiteral,
  ExpressibleByDictionaryLiteral, ExpressibleByArrayLiteral
{
  case null
  case bool(Bool)
  case int(Int64)
  case number(Double)
  case string(String)
  case array([JSON])
  case object([String: JSON])
  package init(stringLiteral value: String) { self = .string(value) }
  package init(integerLiteral value: Int64) { self = .int(value) }
  package init(dictionaryLiteral elements: (String, JSON)...) {
    self = .object(Dictionary(uniqueKeysWithValues: elements))
  }
  package init(arrayLiteral elements: JSON...) { self = .array(elements) }
  package init(from decoder: Decoder) throws {
    let c = try decoder.singleValueContainer()
    if c.decodeNil() {
      self = .null
    } else if let v = try? c.decode(Bool.self) {
      self = .bool(v)
    } else if let v = try? c.decode(Int64.self) {
      self = .int(v)
    } else if let v = try? c.decode(Double.self) {
      self = .number(v)
    } else if let v = try? c.decode(String.self) {
      self = .string(v)
    } else if let v = try? c.decode([JSON].self) {
      self = .array(v)
    } else {
      self = .object(try c.decode([String: JSON].self))
    }
  }
  package func encode(to encoder: Encoder) throws {
    var c = encoder.singleValueContainer()
    switch self {
    case .null: try c.encodeNil()
    case .bool(let v): try c.encode(v)
    case .int(let v): try c.encode(v)
    case .number(let v): try c.encode(v)
    case .string(let v): try c.encode(v)
    case .array(let v): try c.encode(v)
    case .object(let v): try c.encode(v)
    }
  }
  package subscript(_ key: String) -> JSON { fields[key] ?? .null }
  package var fields: [String: JSON] {
    if case .object(let v) = self { return v }
    return [:]
  }
  package var rows: [JSON] {
    if case .array(let v) = self { return v }
    return []
  }
  package var text: String {
    if case .string(let v) = self { return v }
    return ""
  }
  package var count: Int64 {
    if case .int(let v) = self { return v }
    return 0
  }
  package var double: Double {
    if case .number(let v) = self { return v }
    return Double(count)
  }
  package var pretty: String {
    let e = JSONEncoder()
    e.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
    return (try? String(decoding: e.encode(self), as: UTF8.self)) ?? ""
  }
}

package struct ReviewedPlan: Codable {
  package let plan_id: String
  package let fingerprint: String
  package let status: String
  package let blocking_count: Int
  package let counts: [String: Int]
  package var eligible: Bool {
    ["draft", "awaiting_approval", "approved"].contains(status) && blocking_count == 0
      && counts["needs_review", default: 0] == 0 && !fingerprint.isEmpty && !plan_id.isEmpty
  }
  package var reference: JSON {
    ["plan_id": .string(plan_id), "fingerprint": .string(fingerprint)]
  }
}
package struct RPCError: Error, LocalizedError, Codable {
  package let code: Int
  package let message: String
  package var errorDescription: String? { "\(message) (\(code))" }
}
struct Request: Encodable {
  let jsonrpc = "2.0"
  let id: Int
  let method: String
  let params: JSON
}
struct Reply: Decodable {
  let id: Int?
  let method: String?
  let params: JSON?
  let result: JSON?
  let error: RPCError?
}
package enum Format {
  package static func bytes(_ n: Int64) -> String {
    ByteCountFormatter.string(fromByteCount: n, countStyle: .decimal)
  }
  package static func eta(_ seconds: Int64) -> String {
    guard seconds >= 0 else { return "Estimating" }
    return seconds < 60 ? "\(seconds)s" : "\(seconds / 60)m \(seconds % 60)s"
  }
}
