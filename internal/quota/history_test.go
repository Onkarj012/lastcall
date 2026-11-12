package quota

import (
	"context"
	"testing"

	"lastcall/internal/cpa"
)

type fixtureCaller string

// APICall returns one Codex usage fixture without making network requests.
func (f fixtureCaller) APICall(context.Context, cpa.APICallRequest) (cpa.APICallResponse, error) {
	return cpa.APICallResponse{StatusCode: 200, Body: string(f)}, nil
}

// TestCodexMeasurementProvenance keeps inferred quota and relative reset times distinguishable.
func TestCodexMeasurementProvenance(t *testing.T) {
	for _, tc := range []struct {
		body                string
		inferred, estimated bool
	}{
		{`{"rate_limit":{"allowed":false,"primary_window":{"reset_after_seconds":300}}}`, true, true},
		{`{"rate_limit":{"primary_window":{"used_percent":60,"reset_at":1794355200}}}`, false, false},
	} {
		r := Fetch(context.Background(), fixtureCaller(tc.body), cpa.Credential{Provider: "codex"})
		if r.Error != "" || len(r.Windows) != 1 {
			t.Fatal(r)
		}
		w := r.Windows[0]
		if w.Inferred != tc.inferred || w.ResetEstimated != tc.estimated || w.ID == "" {
			t.Fatal(w)
		}
	}
}
