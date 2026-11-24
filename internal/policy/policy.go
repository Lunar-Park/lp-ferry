// Package policy decides what an agent may do without a human. Agents can
// never delete sources or overwrite destinations regardless of policy:
// there is no such operation anywhere in Ferry to grant.
package policy

import (
	"fmt"
	"strings"

	"github.com/Lunar-Park/lp-ferry/internal/config"
	"github.com/Lunar-Park/lp-ferry/internal/model"
)

// Decision is the policy verdict for starting a plan.
type Decision struct {
	Allowed    bool     `json:"allowed"`     // may start now
	NeedsHuman bool     `json:"needs_human"` // must wait for a human approval
	Reasons    []string `json:"reasons,omitempty"`
}

// IsAgent reports whether an actor is an agent ("agent:<name>").
func IsAgent(actor string) bool { return strings.HasPrefix(actor, "agent:") }

// ValidActor reports an acceptable actor string.
func ValidActor(actor string) bool {
	if actor == "human" {
		return true
	}
	name, ok := strings.CutPrefix(actor, "agent:")
	if !ok || name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		allowed := r == '-' || r == '_' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !allowed {
			return false
		}
	}
	return true
}

// Evaluate decides whether actor may start plan without a human.
func Evaluate(p config.AgentPolicy, actor string, plan *model.Plan) Decision {
	if !IsAgent(actor) {
		return Decision{Allowed: true}
	}
	var reasons []string
	var total int64
	for _, d := range plan.Destinations {
		total += d.RequiredBytes
		switch {
		case d.DestinationID == "" && !p.AllowUnsavedDestinations:
			reasons = append(reasons, fmt.Sprintf("destination %s is not a saved destination; agents need human approval for unsaved destinations", d.Label))
		case d.DestinationID != "" && !p.AutoApproveSavedDestinations:
			reasons = append(reasons, fmt.Sprintf("auto-approval for saved destinations is off (%s)", d.Label))
		}
	}
	if total > p.AutoApproveMaxBytes {
		reasons = append(reasons, fmt.Sprintf("transfer is %d bytes, over the agent auto-approve limit of %d", total, p.AutoApproveMaxBytes))
	}
	if len(reasons) > 0 {
		return Decision{NeedsHuman: true, Reasons: reasons}
	}
	return Decision{Allowed: true}
}
