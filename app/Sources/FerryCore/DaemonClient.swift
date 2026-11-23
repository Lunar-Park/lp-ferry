import Foundation
import Network

/// Each link owns its frame buffer and outstanding reply table on a serial queue.
package final class CommandLink: @unchecked Sendable {
  private let queue = DispatchQueue(label: "dev.lunarpark.ferry.rpc")
  private let path: String
  private var connection: NWConnection?
  private var pending = [Int: CheckedContinuation<JSON, Error>]()
  private var nextID = 0
  private var buffer = Data()
  private var scannedBytes = 0
  private let event: (JSON) -> Void
  private let closed: () -> Void

  package init(
    path: String, event: @escaping (JSON) -> Void = { _ in }, closed: @escaping () -> Void = {}
  ) {
    self.path = path
    self.event = event
    self.closed = closed
  }
  private func open() -> NWConnection {
    if let connection { return connection }
    let c = NWConnection(to: .unix(path: path), using: .tcp)
    connection = c
    c.stateUpdateHandler = { [weak self, weak c] state in
      guard let self, let c, c === self.connection else { return }
      switch state {
      case .failed(let e), .waiting(let e): self.disconnect(c, e)
      default: break
      }
    }
    c.start(queue: queue)
    read(c)
    return c
  }
  private func disconnect(_ c: NWConnection, _ error: Error) {
    guard c === connection else { return }
    connection = nil
    c.cancel()
    buffer.removeAll()
    scannedBytes = 0
    let requests = pending
    pending.removeAll()
    for request in requests.values { request.resume(throwing: error) }
    closed()
  }
  private func read(_ c: NWConnection) {
    c.receive(minimumIncompleteLength: 1, maximumLength: 65536) {
      [weak self] bytes, _, done, error in
      guard let self, c === self.connection else { return }
      if let bytes { self.buffer.append(bytes) }
      do {
        while let end = self.buffer.dropFirst(self.scannedBytes).firstIndex(of: 10) {
          self.scannedBytes = 0
          let reply = try JSONDecoder().decode(Reply.self, from: self.buffer[..<end])
          self.buffer.removeSubrange(...end)
          if let id = reply.id, let waiter = self.pending.removeValue(forKey: id) {
            if let error = reply.error {
              waiter.resume(throwing: error)
            } else {
              waiter.resume(returning: reply.result ?? .null)
            }
          } else if reply.method == "event", let params = reply.params {
            self.event(params)
          }
        }
        self.scannedBytes = self.buffer.count
      } catch {
        self.disconnect(c, error)
        return
      }
      if done || error != nil {
        self.disconnect(c, error ?? NWError.posix(.ECONNRESET))
      } else {
        self.read(c)
      }
    }
  }
  package func call(_ method: String, _ params: JSON = [:]) async throws -> JSON {
    try await withCheckedThrowingContinuation { waiter in
      queue.async {
        self.nextID += 1
        let id = self.nextID
        do {
          var bytes = try JSONEncoder().encode(Request(id: id, method: method, params: params))
          bytes.append(10)
          let c = self.open()
          self.pending[id] = waiter
          c.send(
            content: bytes,
            completion: .contentProcessed { error in
              if let error { self.disconnect(c, error) }
            })
        } catch { waiter.resume(throwing: error) }
      }
    }
  }
  package func close() {
    queue.async { if let c = self.connection { self.disconnect(c, CancellationError()) } }
  }
}

package final class EventSubscription {
  package let events: AsyncStream<JSON>
  private let link: CommandLink
  init(_ events: AsyncStream<JSON>, link: CommandLink) {
    self.events = events
    self.link = link
  }
  deinit { link.close() }
  package func close() { link.close() }
}

package struct DaemonClient {
  package static var socketPath: String {
    ProcessInfo.processInfo.environment["FERRY_SOCKET"] ?? NSHomeDirectory() + "/.ferry/ferry.sock"
  }
  package let commands: CommandLink
  package let path: String
  package init(path: String = Self.socketPath) {
    self.path = path
    commands = CommandLink(path: path)
  }
  package func subscribe() async throws -> EventSubscription {
    let (stream, sink) = AsyncStream<JSON>.makeStream()
    let link = CommandLink(path: path, event: { sink.yield($0) }, closed: { sink.finish() })
    sink.onTermination = { _ in link.close() }
    _ = try await link.call("events.subscribe", ["topics": ["*"]])
    return EventSubscription(stream, link: link)
  }
  package func launchBundledDaemon() throws -> Process {
    let process = Process()
    process.executableURL = Bundle.main.resourceURL?.appendingPathComponent("bin/ferry")
    process.arguments = ["daemon", "--socket", path]
    try process.run()
    return process
  }
}
