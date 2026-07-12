package main

import (
	"encoding/json"
	"testing"
)

// SupportHealthMetrics is a JSON scalar, not an object type, so nothing in the
// GraphQL layer pins its shape — these payloads mirror real API responses.
func TestWorkspaceTemplateDecodesSupportHealth(t *testing.T) {
	// Template with graded threads (csat can be null on its own).
	var graded workspaceTemplate
	payload := `{"id":"t1","name":"PowerSync","supportHealthMetrics":{"csat":null,"solved":100,"aggregateHealth":100}}`
	if err := json.Unmarshal([]byte(payload), &graded); err != nil {
		t.Fatal(err)
	}
	sh := graded.SupportHealth
	if sh == nil || sh.Solved == nil || *sh.Solved != 100 {
		t.Errorf("solved = %+v, want 100", sh)
	}
	if sh.Csat != nil {
		t.Errorf("csat = %v, want nil", *sh.Csat)
	}
	if sh.AggregateHealth == nil || *sh.AggregateHealth != 100 {
		t.Errorf("aggregateHealth = %+v, want 100", sh.AggregateHealth)
	}

	// No threads to grade: the API sends the object with all-null members
	// (or omits it entirely) — both must land as "no metrics".
	for _, payload := range []string{
		`{"id":"t2","supportHealthMetrics":{"csat":null,"solved":null,"aggregateHealth":null}}`,
		`{"id":"t3","supportHealthMetrics":null}`,
	} {
		var ungraded workspaceTemplate
		if err := json.Unmarshal([]byte(payload), &ungraded); err != nil {
			t.Fatal(err)
		}
		if sh := ungraded.SupportHealth; sh != nil && (sh.Solved != nil || sh.Csat != nil || sh.AggregateHealth != nil) {
			t.Errorf("ungraded template %s decoded metrics: %+v", ungraded.ID, sh)
		}
	}
}
