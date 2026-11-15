package lanes

import (
	"errors"
	"reflect"
	"testing"
)

func TestMergeLayers(t *testing.T) {
	base := map[string]any{
		"name":  "default",
		"agent": map[string]any{"model": "opus", "effort": "high"},
		"hooks": map[string]any{"on_lane_created": []any{"a", "b"}},
	}
	over := map[string]any{
		"agent": map[string]any{"effort": "low"},
		"hooks": map[string]any{"on_lane_created": []any{"c"}},
		"extra": map[string]any{"nested": map[string]any{"key": int64(1)}},
	}
	got := MergeLayers(base, over)
	want := map[string]any{
		"name":  "default",
		"agent": map[string]any{"model": "opus", "effort": "low"},
		"hooks": map[string]any{"on_lane_created": []any{"c"}},
		"extra": map[string]any{"nested": map[string]any{"key": int64(1)}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if base["agent"].(map[string]any)["effort"] != "high" {
		t.Error("merging changed a layer")
	}
}

// agentGates is a layer that sets agent.gates, as the TOML decoder reads a list: a slice of any.
func agentGates(gates ...any) map[string]any {
	return map[string]any{"agent": map[string]any{"gates": gates}}
}

func TestMergeLayersJoinsGates(t *testing.T) {
	tests := []struct {
		name   string
		layers []map[string]any
		want   []any
	}{
		{name: "one layer", layers: []map[string]any{agentGates("a", "b")}, want: []any{"a", "b"}},
		{name: "a later layer adds", layers: []map[string]any{agentGates("a"), agentGates("b")}, want: []any{"a", "b"}},
		{name: "a later layer says none", layers: []map[string]any{agentGates("a"), agentGates()}, want: []any{"a"}},
		{name: "a later layer replaces in vain", layers: []map[string]any{agentGates("a", "b"), agentGates("c")}, want: []any{"a", "b", "c"}},
		{name: "an order the layers give", layers: []map[string]any{agentGates("b"), agentGates("a", "b")}, want: []any{"b", "a"}},
		{name: "a name said twice in one layer", layers: []map[string]any{agentGates("a", "a")}, want: []any{"a"}},
		{name: "three layers", layers: []map[string]any{agentGates("a"), {"name": "x"}, agentGates("b", "a"), agentGates("c")}, want: []any{"a", "b", "c"}},
		{name: "an empty list is a list", layers: []map[string]any{agentGates()}, want: []any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeLayers(tt.layers...)
			if gates := got["agent"].(map[string]any)["gates"]; !reflect.DeepEqual(gates, tt.want) {
				t.Errorf("gates %#v, want %#v", gates, tt.want)
			}
		})
	}
	// The union is its own list: a later merge never changes a layer, and a layer never changes the union.
	first, second := agentGates("a"), agentGates("b")
	got := MergeLayers(first, second)
	got["agent"].(map[string]any)["gates"].([]any)[0] = "changed"
	if first["agent"].(map[string]any)["gates"].([]any)[0] != "a" {
		t.Error("changing the union changed a layer")
	}
}

// A gate list a layer sets to something that is no list stays, so the settings refuse it, and no later layer
// replaces it with a list that leaves out an earlier layer's gates.
func TestMergeLayersKeepsAGateListThatIsNoList(t *testing.T) {
	notAList := map[string]any{"agent": map[string]any{"gates": "Edit"}}
	for name, layers := range map[string][]map[string]any{
		"a string over a list":         {agentGates("a"), notAList},
		"a list over a string":         {notAList, agentGates("a")},
		"a table over a list":          {agentGates("a"), {"agent": map[string]any{"gates": map[string]any{"a": true}}}},
		"a list, a string, and a list": {agentGates("a"), notAList, agentGates("b")},
	} {
		t.Run(name, func(t *testing.T) {
			got := MergeLayers(layers...)
			if _, isList := got["agent"].(map[string]any)["gates"].([]any); isList {
				t.Errorf("the gates are a list again: %#v", got)
			}
		})
	}
}

func TestProfileDigest(t *testing.T) {
	profile := Profile{Name: "default", Hooks: map[string][]string{"b": {"2"}, "a": {"1"}}}
	same := Profile{Name: "default", Hooks: map[string][]string{"a": {"1"}, "b": {"2"}}}
	first, err := profile.Digest()
	if err != nil {
		t.Fatal(err)
	}
	second, err := same.Digest()
	if err != nil || first != second {
		t.Errorf("digests %s and %s: %v", first, second, err)
	}
	same.Agent.Model = "haiku"
	third, err := same.Digest()
	if err != nil || third == first {
		t.Errorf("a change kept digest %s: %v", third, err)
	}
}

func TestPlace(t *testing.T) {
	settings := WorktreeSettings{Root: "../{repo}-lanes", BranchPrefix: "lane/"}
	tests := []struct {
		name       string
		settings   WorktreeSettings
		repository string
		lane       string
		branch     string
		want       Placement
		wantErr    bool
	}{
		{name: "beside the repository", settings: settings, repository: "/work/checkout", lane: "l1",
			want: Placement{Path: "/work/checkout-lanes/l1", Branch: "lane/l1"}},
		{name: "a branch the ledger assigned", settings: settings, repository: "/work/checkout", lane: "l1", branch: "lane/retry-t1",
			want: Placement{Path: "/work/checkout-lanes/l1", Branch: "lane/retry-t1"}},
		{name: "an absolute root", settings: WorktreeSettings{Root: "/lanes/{repo}", BranchPrefix: "x/"}, repository: "/work/app", lane: "l1",
			want: Placement{Path: "/lanes/app/l1", Branch: "x/l1"}},
		{name: "a lane with a path in it", settings: settings, repository: "/work/app", lane: "../escape", wantErr: true},
		{name: "a relative repository", settings: settings, repository: "work/app", lane: "l1", wantErr: true},
		{name: "no root", settings: WorktreeSettings{}, repository: "/work/app", lane: "l1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Place(tt.settings, tt.repository, tt.lane, tt.branch)
			if tt.wantErr {
				if !errors.Is(err, ErrBadProfile) {
					t.Fatalf("got %+v, %v", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got %+v, %v", got, err)
			}
		})
	}
}
