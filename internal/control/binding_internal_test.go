package control

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/workorders"
)

// TestAChainOfReworkIsReworkedAtItsTip holds the work a decision reworks: of a finished work order and the finished
// rework of it, the rework, because its rework covers the work it redid.
func TestAChainOfReworkIsReworkedAtItsTip(t *testing.T) {
	order := func(id, of string) workorders.WorkOrder {
		return workorders.WorkOrder{ID: id, Kind: workorders.KindChange, State: workorders.Done, ReworkOf: of}
	}
	ids := func(orders []workorders.WorkOrder) []string {
		var out []string
		for _, o := range orders {
			out = append(out, o.ID)
		}
		return out
	}
	tests := []struct {
		name     string
		finished []workorders.WorkOrder
		want     []string
	}{
		{"nothing", nil, nil},
		{"one work order", []workorders.WorkOrder{order("a", "")}, []string{"a"}},
		{"two apart", []workorders.WorkOrder{order("a", ""), order("b", "")}, []string{"a", "b"}},
		{"a work order and its rework", []workorders.WorkOrder{order("a", ""), order("a2", "a")}, []string{"a2"}},
		{"the rework listed first", []workorders.WorkOrder{order("a2", "a"), order("a", "")}, []string{"a2"}},
		{"a chain of three", []workorders.WorkOrder{order("a", ""), order("a2", "a"), order("a3", "a2")}, []string{"a3"}},
		{"a rework whose work order is not reached", []workorders.WorkOrder{order("a2", "a")}, []string{"a2"}},
		{"two chains", []workorders.WorkOrder{order("a", ""), order("a2", "a"), order("b", ""), order("b2", "b")}, []string{"a2", "b2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ids(chainTips(tt.finished)); !slices.Equal(got, tt.want) {
				t.Errorf("chainTips = %v, want %v", got, tt.want)
			}
		})
	}
}
