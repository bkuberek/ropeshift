package planning_test

import (
	"errors"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

// graphOf builds a graph from edges written "from>to", where from depends on to, and from nodes that
// stand alone.
func graphOf(alone []string, edges ...[2]string) *planning.Graph {
	g := planning.NewGraph()
	for _, id := range alone {
		g.AddNode(id)
	}
	for _, edge := range edges {
		g.AddEdge(edge[0], edge[1])
	}
	return g
}

func TestCycleFindsNothingInAGraphWithoutOne(t *testing.T) {
	g := graphOf([]string{"solo"}, [2]string{"c", "b"}, [2]string{"b", "a"}, [2]string{"c", "a"})
	if cycle := g.Cycle(); cycle != nil {
		t.Fatalf("cycle %v", cycle)
	}
}

func TestCycleNamesTheNodesInDependencyOrder(t *testing.T) {
	cases := []struct {
		name  string
		edges [][2]string
		want  []string
	}{
		{"a node that depends on itself", [][2]string{{"a", "a"}}, []string{"a"}},
		{"two nodes", [][2]string{{"b", "a"}, {"a", "b"}}, []string{"a", "b"}},
		{"three nodes start at the smallest ID whatever the edges' order", [][2]string{{"c", "a"}, {"a", "b"}, {"b", "c"}}, []string{"a", "b", "c"}},
		{"a tail into a cycle is not part of it", [][2]string{{"z", "d"}, {"d", "e"}, {"e", "f"}, {"f", "d"}}, []string{"d", "e", "f"}},
		{"two cycles report the one that starts at the smallest ID", [][2]string{{"x", "y"}, {"y", "x"}, {"b", "c"}, {"c", "b"}}, []string{"b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := graphOf(nil, tc.edges...)
			if got := g.Cycle(); !slices.Equal(got, tc.want) {
				t.Fatalf("cycle %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFormatCycleClosesTheLoop(t *testing.T) {
	if got := planning.FormatCycle([]string{"a", "b", "c"}); got != "a -> b -> c -> a" {
		t.Fatalf("got %q", got)
	}
	if got := planning.FormatCycle([]string{"a"}); got != "a -> a" {
		t.Fatalf("got %q", got)
	}
}

func TestLayersRunTheWorkThatDependsOnNothingFirst(t *testing.T) {
	cases := []struct {
		name  string
		alone []string
		edges [][2]string
		want  [][]string
	}{
		{"an empty graph has no layers", nil, nil, nil},
		{"independent work shares one layer", []string{"b", "a", "c"}, nil, [][]string{{"a", "b", "c"}}},
		{"a chain has a layer for each link", nil, [][2]string{{"c", "b"}, {"b", "a"}}, [][]string{{"a"}, {"b"}, {"c"}}},
		{"a node sits one layer past its deepest dependency", nil,
			[][2]string{{"d", "b"}, {"d", "c"}, {"b", "a"}, {"c", "a"}, {"e", "a"}, {"f", "d"}, {"f", "a"}},
			[][]string{{"a"}, {"b", "c", "e"}, {"d"}, {"f"}}},
		{"a repeated edge counts once", []string{"x"}, [][2]string{{"b", "a"}, {"b", "a"}}, [][]string{{"a", "x"}, {"b"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layers, err := graphOf(tc.alone, tc.edges...).Layers()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.EqualFunc(layers, tc.want, slices.Equal[[]string]) {
				t.Fatalf("layers %v, want %v", layers, tc.want)
			}
		})
	}
}

func TestLayersRefuseACycleAndNameIt(t *testing.T) {
	g := graphOf([]string{"free"}, [2]string{"a", "b"}, [2]string{"b", "a"}, [2]string{"after", "a"})
	layers, err := g.Layers()
	var cyclic *planning.CycleError
	if !errors.As(err, &cyclic) {
		t.Fatalf("layers %v, err %v", layers, err)
	}
	if !slices.Equal(cyclic.Nodes, []string{"a", "b"}) {
		t.Fatalf("cycle %v", cyclic.Nodes)
	}
}

func weights(w map[string]int) func(string) int {
	return func(id string) int { return w[id] }
}

func TestCriticalPathIsTheHeaviestChainOfWorkNotDone(t *testing.T) {
	cases := []struct {
		name      string
		alone     []string
		edges     [][2]string
		weight    map[string]int
		wantPath  []string
		wantTotal int
	}{
		{"an empty graph has none", nil, nil, nil, nil, 0},
		{"one node is its own path", []string{"a"}, nil, map[string]int{"a": 2}, []string{"a"}, 2},
		{"the heavier branch wins, in the order the work runs", nil,
			[][2]string{{"d", "b"}, {"d", "c"}, {"b", "a"}, {"c", "a"}},
			map[string]int{"a": 1, "b": 1, "c": 3, "d": 2}, []string{"a", "c", "d"}, 6},
		{"a tie goes to the smaller ID at each step", nil,
			[][2]string{{"d", "b"}, {"d", "c"}, {"b", "a"}, {"c", "a"}},
			map[string]int{"a": 1, "b": 2, "c": 2, "d": 1}, []string{"a", "b", "d"}, 4},
		{"a tie between ends goes to the smaller ID", []string{"y", "x"}, nil, map[string]int{"x": 1, "y": 1}, []string{"x"}, 1},
		{"work that is done weighs nothing and is left out", nil,
			[][2]string{{"c", "b"}, {"b", "a"}}, map[string]int{"a": 0, "b": 3, "c": 1}, []string{"b", "c"}, 4},
		{"a chain cut by done work is two chains, and the heavier counts", nil,
			[][2]string{{"e", "d"}, {"d", "c"}, {"c", "b"}, {"b", "a"}}, map[string]int{"a": 2, "b": 1, "c": 0, "d": 1, "e": 1}, []string{"a", "b"}, 3},
		{"all work done leaves no path", nil, [][2]string{{"b", "a"}}, map[string]int{"a": 0, "b": 0}, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, total, err := graphOf(tc.alone, tc.edges...).CriticalPath(weights(tc.weight))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(path, tc.wantPath) || total != tc.wantTotal {
				t.Fatalf("path %v of %d, want %v of %d", path, total, tc.wantPath, tc.wantTotal)
			}
		})
	}
}

func TestCriticalPathRefusesACycle(t *testing.T) {
	g := graphOf(nil, [2]string{"a", "b"}, [2]string{"b", "a"})
	if _, _, err := g.CriticalPath(func(string) int { return 1 }); err == nil {
		t.Fatal("a cycle has no critical path")
	}
}

func TestSizeWeightCountsSmallAsOneMediumAsTwoAndLargeAsThree(t *testing.T) {
	cases := map[string]int{
		planning.SizeSmall: 1, planning.SizeMedium: 2, planning.SizeLarge: 3, planning.SizeUnknown: 1, "": 1, "huge": 1,
	}
	for size, want := range cases {
		if got := planning.SizeWeight(size); got != want {
			t.Errorf("size %q weighs %d, want %d", size, got, want)
		}
	}
}

func order(id string, dependsOn ...string) planning.ShapeOrder {
	return planning.ShapeOrder{ID: id, DependsOn: dependsOn}
}

func TestShapeCycleSeesEveryKindOfDependency(t *testing.T) {
	cases := []struct {
		name  string
		shape planning.Shape
		want  []string
	}{
		{"a plan with no loop", planning.Shape{
			Milestones: []planning.ShapeMilestone{{ID: "m1"}, {ID: "m2", DependsOn: []string{"m1"}}},
			Contracts: []planning.ShapeContract{
				{ID: "base", Milestone: "m1", Orders: []planning.ShapeOrder{order("b1")}},
				{ID: "app", Milestone: "m2", Uses: []string{"base"}, Orders: []planning.ShapeOrder{order("a1", "b1")}},
			},
		}, nil},
		{"milestones that depend on each other", planning.Shape{
			Milestones: []planning.ShapeMilestone{{ID: "m1", DependsOn: []string{"m2"}}, {ID: "m2", DependsOn: []string{"m1"}}},
		}, []string{"milestone:m1", "milestone:m2"}},
		{"milestones in a longer loop", planning.Shape{
			Milestones: []planning.ShapeMilestone{
				{ID: "c", DependsOn: []string{"a"}}, {ID: "a", DependsOn: []string{"b"}}, {ID: "b", DependsOn: []string{"c"}},
			},
		}, []string{"milestone:a", "milestone:b", "milestone:c"}},
		{"contracts that use each other", planning.Shape{
			Contracts: []planning.ShapeContract{{ID: "x", Uses: []string{"y"}}, {ID: "y", Uses: []string{"x"}}},
		}, []string{"contract:x", "contract:y"}},
		{"work orders that depend on each other across contracts", planning.Shape{
			Contracts: []planning.ShapeContract{
				{ID: "x", Orders: []planning.ShapeOrder{order("x1", "y1")}},
				{ID: "y", Orders: []planning.ShapeOrder{order("y1", "x1")}},
			},
		}, []string{"work_order:x1", "work_order:y1"}},
		{"work that is earlier by milestone and later by an explicit dependency", planning.Shape{
			Milestones: []planning.ShapeMilestone{{ID: "m1"}, {ID: "m2", DependsOn: []string{"m1"}}},
			Contracts: []planning.ShapeContract{
				{ID: "early", Milestone: "m1", Orders: []planning.ShapeOrder{order("e1", "l1")}},
				{ID: "late", Milestone: "m2", Orders: []planning.ShapeOrder{order("l1")}},
			},
		}, []string{"work_order:e1", "work_order:l1"}},
		{"a shared contract in a milestone that waits for the milestone of the contract that uses it", planning.Shape{
			Milestones: []planning.ShapeMilestone{{ID: "m1"}, {ID: "m2", DependsOn: []string{"m1"}}},
			Contracts: []planning.ShapeContract{
				{ID: "app", Milestone: "m1", Uses: []string{"base"}, Orders: []planning.ShapeOrder{order("a1")}},
				{ID: "base", Milestone: "m2", Orders: []planning.ShapeOrder{order("b1")}},
			},
		}, []string{"work_order:a1", "work_order:b1"}},
		{"a reference to something the plan does not hold is no edge", planning.Shape{
			Milestones: []planning.ShapeMilestone{{ID: "m1", DependsOn: []string{"gone"}}},
			Contracts:  []planning.ShapeContract{{ID: "x", Milestone: "gone", Uses: []string{"gone"}, Orders: []planning.ShapeOrder{order("x1", "gone")}}},
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.shape.Cycle(); !slices.Equal(got, tc.want) {
				t.Fatalf("cycle %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShapeExpandTurnsMilestonesAndSharedContractsIntoEdgesBetweenWorkOrders(t *testing.T) {
	shape := planning.Shape{
		Milestones: []planning.ShapeMilestone{{ID: "m1"}, {ID: "m2", DependsOn: []string{"m1"}}, {ID: "m3", DependsOn: []string{"m1", "m2"}}},
		Contracts: []planning.ShapeContract{
			{ID: "base", Milestone: "m1", Orders: []planning.ShapeOrder{order("b1"), order("b2")}},
			{ID: "solo", Milestone: "m1", Orders: []planning.ShapeOrder{order("s1")}},
			{ID: "app", Milestone: "m2", Uses: []string{"base"}, Orders: []planning.ShapeOrder{order("a1")}},
			{ID: "loose", Orders: []planning.ShapeOrder{order("l1")}},
			{ID: "last", Milestone: "m3", Uses: []string{"loose", "gone"}, Orders: []planning.ShapeOrder{order("z1", "b1")}},
		},
	}
	type edge = planning.ShapeEdge
	want := []edge{
		{Order: "a1", DependsOn: "b1", Kind: planning.DependencyMilestone},
		{Order: "a1", DependsOn: "b1", Kind: planning.DependencySharedContract},
		{Order: "a1", DependsOn: "b2", Kind: planning.DependencyMilestone},
		{Order: "a1", DependsOn: "b2", Kind: planning.DependencySharedContract},
		{Order: "a1", DependsOn: "s1", Kind: planning.DependencyMilestone},
		{Order: "z1", DependsOn: "a1", Kind: planning.DependencyMilestone},
		{Order: "z1", DependsOn: "b1", Kind: planning.DependencyMilestone},
		{Order: "z1", DependsOn: "b2", Kind: planning.DependencyMilestone},
		{Order: "z1", DependsOn: "l1", Kind: planning.DependencySharedContract},
		{Order: "z1", DependsOn: "s1", Kind: planning.DependencyMilestone},
	}
	if got := shape.Expand(); !slices.Equal(got, want) {
		t.Fatalf("edges\n%v\nwant\n%v", got, want)
	}
	if got := (planning.Shape{}).Expand(); len(got) != 0 {
		t.Fatalf("an empty plan expands to %v", got)
	}
}
