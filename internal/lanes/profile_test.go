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
