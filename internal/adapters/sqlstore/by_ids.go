package sqlstore

import (
	"fmt"
	"slices"
	"strings"
)

// idSlots is how many IDs one statement of a read by ID names. A read of more IDs runs the statement once for
// each slice of that many, and a slice of fewer fills the slots it leaves with its first ID. Each statement is one
// the store prepares once, in any dialect, and it finds each row through the key of its table.
const idSlots = 32

// idList is the list a statement of a read by ID compares an ID with: `(:id_0, ..., :id_31)`.
var idList = func() string {
	slots := make([]string, idSlots)
	for i := range slots {
		slots[i] = fmt.Sprintf(":id_%d", i)
	}
	return "(" + strings.Join(slots, ", ") + ")"
}()

// idSlices sorts some IDs, keeps one of each, and cuts them into the parameters of the statements of a read by
// ID, one for each slice of idSlots. No IDs read nothing.
func idSlices(ids []string) []params {
	held := slices.Compact(slices.Sorted(slices.Values(ids)))
	var out []params
	for slice := range slices.Chunk(held, idSlots) {
		p := params{}
		for i := range idSlots {
			p[fmt.Sprintf("id_%d", i)] = slice[min(i, len(slice)-1)]
		}
		out = append(out, p)
	}
	return out
}
