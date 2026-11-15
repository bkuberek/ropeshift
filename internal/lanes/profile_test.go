package lanes

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
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
	got, err := MergeLayers(base, over)
	if err != nil {
		t.Fatal(err)
	}
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
		{name: "gates under a table spelled another way", layers: []map[string]any{agentGates("a"), {"Agent": map[string]any{"gates": []any{"b"}}}}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MergeLayers(tt.layers...)
			if tt.want == nil {
				if !errors.Is(err, ErrBadProfile) {
					t.Fatalf("got %v, %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if gates := got["agent"].(map[string]any)["gates"]; !reflect.DeepEqual(gates, tt.want) {
				t.Errorf("gates %#v, want %#v", gates, tt.want)
			}
		})
	}
	// The union is its own list: a later merge never changes a layer, and a layer never changes the union.
	first, second := agentGates("a"), agentGates("b")
	got, err := MergeLayers(first, second)
	if err != nil {
		t.Fatal(err)
	}
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
			got, err := MergeLayers(layers...)
			if err != nil {
				t.Fatal(err)
			}
			if _, isList := got["agent"].(map[string]any)["gates"].([]any); isList {
				t.Errorf("the gates are a list again: %#v", got)
			}
		})
	}
}

func TestMergeLayersRefusesTwoSpellingsOfOneKey(t *testing.T) {
	tests := []struct {
		name      string
		layers    []map[string]any
		spellings [2]string
		table     string
	}{
		{name: "in one layer", layers: []map[string]any{{"hooks": map[string]any{}, "Hooks": map[string]any{}}}, spellings: [2]string{"Hooks", "hooks"}},
		{name: "across layers", layers: []map[string]any{{"hooks": map[string]any{}}, {"HOOKS": map[string]any{}}}, spellings: [2]string{"hooks", "HOOKS"}},
		{name: "in a table", layers: []map[string]any{{"worktree": map[string]any{"root": "a", "ROOT": "b"}}}, spellings: [2]string{"ROOT", "root"}, table: "[worktree]"},
		{name: "in a table across layers", layers: []map[string]any{{"worktree": map[string]any{"root": "a"}}, {"worktree": map[string]any{"Root": "b"}}}, spellings: [2]string{"root", "Root"}, table: "[worktree]"},
		{name: "in a table the first layer made", layers: []map[string]any{{"a": map[string]any{"b": map[string]any{"c": int64(1)}}}, {"a": map[string]any{"B": map[string]any{}}}}, spellings: [2]string{"b", "B"}, table: "[a]"},
		{name: "in a table of a table", layers: []map[string]any{{"a": map[string]any{"b": map[string]any{"c": int64(1)}}}, {"a": map[string]any{"b": map[string]any{"C": int64(2)}}}}, spellings: [2]string{"c", "C"}, table: "[a.b]"},
		{name: "with the Kelvin sign", layers: []map[string]any{{"hooks": 1}, {"hoo\u212as": 2}}, spellings: [2]string{"hooks", "hoo\u212as"}},
		{name: "with the long s", layers: []map[string]any{{"hooks": 1}, {"hookſ": 2}}, spellings: [2]string{"hooks", "hookſ"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for run := range 100 {
				_, err := MergeLayers(tt.layers...)
				if !errors.Is(err, ErrBadProfile) {
					t.Fatalf("run %d: %v", run, err)
				}
				want := fmt.Sprintf("%q and %q spell one key", tt.spellings[0], tt.spellings[1])
				if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), tt.table) {
					t.Fatalf("run %d: error %q, want %q in %q", run, err, want, tt.table)
				}
			}
			// A layer alone is refused only when it holds both spellings itself.
			if err := CheckSpellings(tt.layers[0]); (len(tt.layers) == 1) != (err != nil) {
				t.Errorf("CheckSpellings of the first layer gave %v", err)
			}
		})
	}
}

// foldKey must agree with strings.EqualFold, which is how the profile's decoder reads a key.
func TestFoldKeyAgreesWithEqualFold(t *testing.T) {
	keys := []string{"", "a", "A", "hooks", "Hooks", "HOOKS", "hookſ", "ſ", "s", "S", "K", "k", "K", "ǆ", "ǅ", "Ǆ", "σ", "ς", "Σ", "ß", "ẞ", "ss", "max_review_rounds", "MAX_REVIEW_ROUNDS", "a.b", "a\xffb", "a\xfeb"}
	for _, a := range keys {
		for _, b := range keys {
			if same := foldKey(a) == foldKey(b); same != strings.EqualFold(a, b) {
				t.Errorf("foldKey says %q and %q are equal=%v, strings.EqualFold says %v", a, b, same, !same)
			}
		}
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
