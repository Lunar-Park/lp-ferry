import Foundation

package struct Location: Codable, Hashable, Sendable {
  package let path: String?
  package let peer_id: String?
  package let destination_id: String?
  package let label: String
  package let confirm_binding: Bool?

  package init(path: String, peerID: String? = nil, label: String? = nil) {
    self.path = path
    self.peer_id = peerID
    self.label = label ?? URL(fileURLWithPath: path).lastPathComponent
    self.destination_id = nil
    self.confirm_binding = nil
  }
  package init(destinationID: String, label: String, confirmed: Bool? = nil) {
    self.destination_id = destinationID
    self.label = label
    self.confirm_binding = confirmed
    self.path = nil
    self.peer_id = nil
  }
}
package struct PlanRequest: Codable {
  let sources: [Location]
  let destinations: [Location]
  let actor: String
  let preset_id: String?
  let conflict_policy: String
  let checksum_algo: String
  let camera_roles: [String: String]

  @MainActor package init(workspace: Store) {
    sources = workspace.sources
    destinations = workspace.targets
    actor = "human"
    preset_id = workspace.preset.isEmpty ? nil : workspace.preset
    conflict_policy = workspace.policy
    checksum_algo = workspace.checksum
    let labels = Set(sources.map(\.label))
    camera_roles = workspace.roles.filter { labels.contains($0.key) && !$0.value.isEmpty }
  }
  package var params: JSON {
    get throws { try JSONDecoder().decode(JSON.self, from: JSONEncoder().encode(self)) }
  }
}
