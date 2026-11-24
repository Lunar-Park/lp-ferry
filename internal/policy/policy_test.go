package policy

import (
	"testing"

	"github.com/Lunar-Park/lp-ferry/internal/config"
	"github.com/Lunar-Park/lp-ferry/internal/model"
)

func TestEvaluate(t *testing.T) {
	p := config.AgentPolicy{AutoApproveSavedDestinations: true, AutoApproveMaxBytes: 1000}
	saved := &model.Plan{Destinations: []model.PlanDestination{{DestinationID: "dst_1", RequiredBytes: 500}}}
	unsaved := &model.Plan{Destinations: []model.PlanDestination{{Label: "x", RequiredBytes: 10}}}
	big := &model.Plan{Destinations: []model.PlanDestination{{DestinationID: "dst_1", RequiredBytes: 5000}}}
	if d := Evaluate(p, "human", unsaved); !d.Allowed {
		t.Fatal("humans are not policy-gated")
	}
	if d := Evaluate(p, "agent:claude", saved); !d.Allowed {
		t.Fatalf("%+v", d)
	}
	if d := Evaluate(p, "agent:claude", unsaved); d.Allowed || !d.NeedsHuman {
		t.Fatalf("unsaved: %+v", d)
	}
	if d := Evaluate(p, "agent:claude", big); d.Allowed || !d.NeedsHuman {
		t.Fatalf("over limit: %+v", d)
	}
	p.AutoApproveSavedDestinations = false
	if d := Evaluate(p, "agent:claude", saved); d.Allowed {
		t.Fatal("auto-approve off must wait for a human")
	}
	for _, a := range []string{"human", "agent:claude", "agent:codex-1.2"} {
		if !ValidActor(a) {
			t.Fatal(a)
		}
	}
	for _, a := range []string{"", "root", "agent:", "agent:a b", "agent:../x"} {
		if ValidActor(a) {
			t.Fatal(a)
		}
	}
}
