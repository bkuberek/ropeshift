package planning

import (
	"cmp"
	"maps"
	"slices"
	"strings"
)

// Graph is a directed graph of named nodes. An edge says one node depends on another, so the work
// of the node it points to comes first. Every answer is deterministic: where two choices are equal,
// the smaller ID is taken (FR-MILE-4, FR-MILE-5).
type Graph struct {
	deps map[string]map[string]bool
}

// NewGraph returns a graph with no nodes.
func NewGraph() *Graph {
	return &Graph{deps: map[string]map[string]bool{}}
}

// AddNode adds a node that may depend on nothing and have nothing depend on it.
func (g *Graph) AddNode(id string) {
	if _, held := g.deps[id]; !held {
		g.deps[id] = map[string]bool{}
	}
}

// AddEdge says node depends on dependsOn, and adds either node the graph lacks. A repeated edge counts
// once.
func (g *Graph) AddEdge(node, dependsOn string) {
	g.AddNode(node)
	g.AddNode(dependsOn)
	g.deps[node][dependsOn] = true
}

// Nodes lists every node by ID.
func (g *Graph) Nodes() []string {
	return slices.Sorted(maps.Keys(g.deps))
}

// DependsOn lists what one node depends on, by ID.
func (g *Graph) DependsOn(node string) []string {
	return slices.Sorted(maps.Keys(g.deps[node]))
}

// CycleError says a graph holds a cycle. Nodes is the cycle in dependency order: each node depends on
// the next, and the last on the first.
type CycleError struct {
	Nodes []string
}

func (e *CycleError) Error() string {
	return "dependency cycle: " + FormatCycle(e.Nodes)
}

// FormatCycle writes a cycle as a closed loop, as in `a -> b -> a`.
func FormatCycle(nodes []string) string {
	if len(nodes) == 0 {
		return ""
	}
	return strings.Join(nodes, " -> ") + " -> " + nodes[0]
}

// Cycle finds a cycle, or reports nil for a graph with none. It starts the search at each node in ID
// order and follows each node's dependencies in ID order, and it names the cycle from its smallest ID,
// so the same graph always reports the same cycle.
func (g *Graph) Cycle() []string {
	const (
		unseen = iota
		onPath
		cleared
	)
	state := map[string]int{}
	var path []string
	var found []string
	var visit func(node string) bool
	visit = func(node string) bool {
		state[node] = onPath
		path = append(path, node)
		for _, next := range g.DependsOn(node) {
			switch state[next] {
			case onPath:
				found = rotateToSmallest(path[slices.Index(path, next):])
				return true
			case unseen:
				if visit(next) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		state[node] = cleared
		return false
	}
	for _, node := range g.Nodes() {
		if state[node] == unseen && visit(node) {
			return found
		}
	}
	return nil
}

// rotateToSmallest returns a copy of a cycle that starts at its smallest ID.
func rotateToSmallest(cycle []string) []string {
	start := 0
	for i, node := range cycle {
		if node < cycle[start] {
			start = i
		}
	}
	return slices.Concat(cycle[start:], cycle[:start])
}

// Layers groups the nodes by how much must come before them: a node with no dependencies is in layer 0,
// and any other node is one layer past its deepest dependency. The nodes of one layer depend on nothing
// in it, so they can run at the same time. A graph with a cycle has no layers.
func (g *Graph) Layers() ([][]string, error) {
	order, err := g.order()
	if err != nil {
		return nil, err
	}
	depth := map[string]int{}
	var layers [][]string
	for _, node := range order {
		at := 0
		for _, dependency := range g.DependsOn(node) {
			at = max(at, depth[dependency]+1)
		}
		depth[node] = at
		for len(layers) <= at {
			layers = append(layers, nil)
		}
		layers[at] = append(layers[at], node)
	}
	for _, layer := range layers {
		slices.Sort(layer)
	}
	return layers, nil
}

// order lists the nodes so that each comes after everything it depends on, or reports the cycle that
// stops that.
func (g *Graph) order() ([]string, error) {
	if cycle := g.Cycle(); cycle != nil {
		return nil, &CycleError{Nodes: cycle}
	}
	var out []string
	done := map[string]bool{}
	var visit func(node string)
	visit = func(node string) {
		if done[node] {
			return
		}
		done[node] = true
		for _, dependency := range g.DependsOn(node) {
			visit(dependency)
		}
		out = append(out, node)
	}
	for _, node := range g.Nodes() {
		visit(node)
	}
	return out, nil
}

// CriticalPath finds the heaviest chain of nodes, in the order the work runs, and its total weight. A
// node with a weight of 0 or less is not on the chain, and the chain does not cross it, so a node
// that is done can weigh 0 and leave the remaining work. Between two chains of one weight it takes the
// one whose nodes have the smaller IDs, looking back from the last node.
func (g *Graph) CriticalPath(weight func(node string) int) ([]string, int, error) {
	order, err := g.order()
	if err != nil {
		return nil, 0, err
	}
	best := map[string]int{}
	before := map[string]string{}
	var last string
	for _, node := range order {
		own := weight(node)
		if own <= 0 {
			continue
		}
		through := ""
		for _, dependency := range g.DependsOn(node) {
			if _, counted := best[dependency]; counted && (through == "" || best[dependency] > best[through]) {
				through = dependency
			}
		}
		best[node] = own
		if through != "" {
			best[node] += best[through]
			before[node] = through
		}
		if last == "" || best[node] > best[last] || best[node] == best[last] && node < last {
			last = node
		}
	}
	if last == "" {
		return nil, 0, nil
	}
	total := best[last]
	var path []string
	for node := last; node != ""; node = before[node] {
		path = append(path, node)
	}
	slices.Reverse(path)
	return path, total, nil
}

// SizeWeight is what a work order's declared size counts for in a critical path: a small one counts 1,
// a medium one 2, and a large one 3. An unknown or missing size counts 1.
func SizeWeight(size string) int {
	switch size {
	case SizeMedium:
		return 2
	case SizeLarge:
		return 3
	}
	return 1
}

// ShapeMilestone is a milestone as the plan's graph sees it.
type ShapeMilestone struct {
	ID        string
	DependsOn []string
}

// ShapeOrder is a work order and the work orders its `depends_on` names.
type ShapeOrder struct {
	ID        string
	DependsOn []string
}

// ShapeContract is a contract with the milestone it is in, the shared contracts it uses, and its
// work orders.
type ShapeContract struct {
	ID        string
	Milestone string
	Uses      []string
	Orders    []ShapeOrder
}

// Shape is how one plan places its work: its milestones, and its contracts with their work orders. It
// holds only what a plan says, so the same code reads a plan from a batch of files and from the
// ledger. A reference to a milestone, contract, or work order the shape does not hold makes no edge.
type Shape struct {
	Milestones []ShapeMilestone
	Contracts  []ShapeContract
}

// ShapeEdge is one dependency that follows from a plan's structure: the work order Order waits for
// the work order DependsOn.
type ShapeEdge struct {
	Order     string
	DependsOn string
	Kind      DependencyKind
}

// Expand works out the dependencies a plan's structure implies (FR-MILE-2, FR-MILE-3). Every work order of
// a contract in milestone M waits for every work order of each contract in each milestone M depends on,
// and every work order of a contract waits for every work order of each shared contract it uses. Edges
// come out once each, ordered by work order, target, and kind.
func (s Shape) Expand() []ShapeEdge {
	milestones := map[string]ShapeMilestone{}
	for _, m := range s.Milestones {
		milestones[m.ID] = m
	}
	contracts := map[string]ShapeContract{}
	inMilestone := map[string][]ShapeOrder{}
	for _, c := range s.Contracts {
		contracts[c.ID] = c
		if c.Milestone != "" {
			inMilestone[c.Milestone] = append(inMilestone[c.Milestone], c.Orders...)
		}
	}
	seen := map[ShapeEdge]bool{}
	var edges []ShapeEdge
	add := func(order ShapeOrder, target ShapeOrder, kind DependencyKind) {
		edge := ShapeEdge{Order: order.ID, DependsOn: target.ID, Kind: kind}
		if order.ID != target.ID && !seen[edge] {
			seen[edge] = true
			edges = append(edges, edge)
		}
	}
	for _, c := range s.Contracts {
		for _, dependency := range milestones[c.Milestone].DependsOn {
			for _, order := range c.Orders {
				for _, target := range inMilestone[dependency] {
					add(order, target, DependencyMilestone)
				}
			}
		}
		for _, use := range c.Uses {
			shared, held := contracts[use]
			if !held {
				continue
			}
			for _, order := range c.Orders {
				for _, target := range shared.Orders {
					add(order, target, DependencySharedContract)
				}
			}
		}
	}
	slices.SortFunc(edges, func(a, b ShapeEdge) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.DependsOn, b.DependsOn), cmp.Compare(a.Kind, b.Kind))
	})
	return edges
}

// Graph builds the graph a cycle can hide in: milestones, contracts, and work orders are nodes, named
// `milestone:<id>`, `contract:<id>`, and `work_order:<id>`, and the edges are the milestone
// dependencies, the shared contracts a contract uses, the work orders' own `depends_on`, and the
// edges Expand derives.
func (s Shape) Graph() *Graph {
	g := NewGraph()
	milestones := map[string]bool{}
	for _, m := range s.Milestones {
		milestones[m.ID] = true
		g.AddNode(shapeNode("milestone", m.ID))
	}
	contracts := map[string]bool{}
	orders := map[string]bool{}
	for _, c := range s.Contracts {
		contracts[c.ID] = true
		g.AddNode(shapeNode("contract", c.ID))
		for _, o := range c.Orders {
			orders[o.ID] = true
			g.AddNode(shapeNode("work_order", o.ID))
		}
	}
	for _, m := range s.Milestones {
		for _, dependency := range m.DependsOn {
			if milestones[dependency] {
				g.AddEdge(shapeNode("milestone", m.ID), shapeNode("milestone", dependency))
			}
		}
	}
	for _, c := range s.Contracts {
		for _, use := range c.Uses {
			if contracts[use] {
				g.AddEdge(shapeNode("contract", c.ID), shapeNode("contract", use))
			}
		}
		for _, o := range c.Orders {
			for _, dependency := range o.DependsOn {
				if orders[dependency] {
					g.AddEdge(shapeNode("work_order", o.ID), shapeNode("work_order", dependency))
				}
			}
		}
	}
	for _, edge := range s.Expand() {
		g.AddEdge(shapeNode("work_order", edge.Order), shapeNode("work_order", edge.DependsOn))
	}
	return g
}

func shapeNode(kind, id string) string { return kind + ":" + id }

// Cycle finds a dependency cycle in a plan, named by the typed IDs of Graph, or reports nil.
func (s Shape) Cycle() []string {
	return s.Graph().Cycle()
}
