package quota

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"lastcall/internal/cpa"
)

type antigravityFixtureCaller struct {
	responses map[string]string
	errors    map[string]error
	calls     []cpa.APICallRequest
}

func (f *antigravityFixtureCaller) APICall(_ context.Context, req cpa.APICallRequest) (cpa.APICallResponse, error) {
	f.calls = append(f.calls, req)
	if err := f.errors[req.URL]; err != nil {
		return cpa.APICallResponse{}, err
	}
	return cpa.APICallResponse{StatusCode: 200, Body: f.responses[req.URL]}, nil
}

func TestAntigravityMergesPartialHostsAndGemini(t *testing.T) {
	c := &antigravityFixtureCaller{responses: map[string]string{
		antigravityHosts[0]: `{"groups":[{"id":"models","displayName":"Claude","buckets":[{"bucketId":"claude-5h","displayName":"Claude","window":"5h","remainingFraction":0.4}]}]}`,
		antigravityHosts[1]: `{"groups":[{"id":"models","displayName":"Claude","buckets":[{"bucketId":"claude-5h","displayName":"Claude","window":"5h","remainingFraction":0.4},{"bucketId":"claude-week","displayName":"Claude","window":"weekly","remainingFraction":0.8}]}]}`,
		antigravityHosts[2]: `{"groups":[{"id":"models","displayName":"Gemini","buckets":[{"bucketId":"gemini-pro","displayName":"Gemini Pro","window":"weekly","remainingFraction":0.7}]}]}`,
		antigravityPlanURL:  `{"paidTier":{"id":"g1-pro-tier","name":"Google AI Pro"},"currentTier":{"id":"standard-tier","name":"Antigravity"}}`,
	}}
	r, err := antigravity(context.Background(), c, cpa.Credential{ProjectID: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Error != "" || r.Plan != "Google AI Pro" || len(r.Windows) != 3 {
		t.Fatalf("unexpected result: %+v", r)
	}
	if r.Windows[0].ID != "claude-5h" || r.Windows[0].GroupID != "models" || r.Windows[0].Group != "Claude" {
		t.Fatalf("missing group/bucket identity: %+v", r.Windows[0])
	}
	if r.Windows[2].ID != "gemini-pro" || r.Windows[2].Kind != "week" {
		t.Fatalf("missing Gemini window: %+v", r.Windows[1])
	}
	if got := c.calls[0].Header["User-Agent"]; got != "antigravity/hub/2.23.0 darwin/arm64" {
		t.Fatalf("unexpected user agent: %q", got)
	}
}

func TestAntigravityDeduplicatesBucketsAndHandlesFractions(t *testing.T) {
	const payload = `{"groups":[{"id":"g","displayName":"Gemini","buckets":[{"id":"gemini-exhausted","window":"5h"},{"id":"gemini-null","remainingFraction":null},{"id":"gemini-invalid","remainingFraction":"oops"},{"id":"gemini-half","remainingFraction":0.5},{"id":"gemini-percent","remainingFraction":"0.5%"},{"id":"gemini-nan","remainingFraction":"NaN"}]}]}`
	c := &antigravityFixtureCaller{responses: map[string]string{antigravityHosts[0]: payload, antigravityHosts[1]: payload}}
	r, err := antigravity(context.Background(), c, cpa.Credential{ProjectID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Windows) != 3 {
		t.Fatalf("got %d windows: %+v", len(r.Windows), r.Windows)
	}
	if r.Windows[0].Left != 0 || r.Windows[1].Left != 0.5 || r.Windows[2].Left != 0.005 {
		t.Fatalf("wrong remaining fractions: %+v", r.Windows)
	}
	if r.Windows[0].ID != "gemini-exhausted" {
		t.Fatalf("missing stable ID: %+v", r.Windows[0])
	}
}

func TestAntigravityMissingGeminiAndPlanFailureKeepQuotas(t *testing.T) {
	c := &antigravityFixtureCaller{
		responses: map[string]string{antigravityHosts[0]: `{"groups":[{"id":"g","displayName":"Claude","buckets":[{"bucketId":"claude-5h","remainingFraction":0.2}]}]}`},
		errors:    map[string]error{antigravityHosts[1]: errors.New("unavailable"), antigravityHosts[2]: errors.New("unavailable"), antigravityPlanURL: errors.New("unavailable")},
	}
	r, err := antigravity(context.Background(), c, cpa.Credential{ProjectID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Warning == "" || r.Error != "" || len(r.Windows) != 1 || r.Plan != "" {
		t.Fatalf("unexpected partial result: %+v", r)
	}
	encoded, err := json.Marshal(r.Windows[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["group_id"]) != `"g"` || string(fields["group"]) != `"Claude"` {
		t.Fatalf("missing JSON fields: %s", encoded)
	}
}

func TestAntigravityZeroUsableWindowsIsError(t *testing.T) {
	c := &antigravityFixtureCaller{responses: map[string]string{
		antigravityHosts[0]: `{"groups":[{"id":"g","buckets":[{"bucketId":"bad","remainingFraction":"invalid"}]}]}`,
		antigravityHosts[1]: `{"groups":[]}`,
		antigravityHosts[2]: `{"groups":[]}`,
	}}
	r, err := antigravity(context.Background(), c, cpa.Credential{ProjectID: "p"})
	if err == nil || err.Error() != "no usable quota windows returned" || len(r.Windows) != 0 {
		t.Fatalf("expected hard error for no usable windows; result=%+v err=%v", r, err)
	}
}

func TestAntigravityMergesGeminiFirstPartialResponse(t *testing.T) {
	c := &antigravityFixtureCaller{responses: map[string]string{
		antigravityHosts[0]: `{"groups":[{"displayName":"Gemini models","buckets":[{"bucketId":"gemini-5h","window":"5h","remainingFraction":0.7}]}]}`,
		antigravityHosts[1]: `{"groups":[{"displayName":"Gemini models","buckets":[{"bucketId":"gemini-week","window":"weekly","remainingFraction":0.4}]},{"displayName":"Claude and GPT models","buckets":[{"bucketId":"claude-5h","window":"5h","remainingFraction":0.9},{"bucketId":"claude-week","window":"weekly","remainingFraction":0.8}]}]}`,
		antigravityHosts[2]: `{"groups":[]}`,
		antigravityPlanURL:  `{"currentTier":{"name":"Pro"}}`,
	}}
	r, err := antigravity(context.Background(), c, cpa.Credential{ProjectID: "p"})
	if err != nil || len(r.Windows) != 4 || r.Warning != "" {
		t.Fatalf("partial Gemini response hid other limits: result=%+v err=%v", r, err)
	}
	c.responses[antigravityHosts[1]] = `{"groups":[]}`
	r, err = antigravity(context.Background(), c, cpa.Credential{ProjectID: "p"})
	if err != nil || len(r.Windows) != 1 || r.Warning == "" {
		t.Fatalf("missing weekly window was not reported: result=%+v err=%v", r, err)
	}
}
