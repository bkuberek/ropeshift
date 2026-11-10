package planning

// Untraced is a criterion that does not serve a requirement the plan has. Requirement is empty for a
// criterion that names none.
type Untraced struct {
	Criterion   string
	Requirement string
}

func (u Untraced) String() string {
	if u.Requirement == "" {
		return "criterion " + u.Criterion + " maps to no requirement"
	}
	return "criterion " + u.Criterion + " names requirement " + u.Requirement + ", which the plan does not have"
}

// Trace holds each criterion to the requirements a plan has: every one must name at least one, and
// every one it names must be there (FR-DOC-5). Criteria come out in the order they are written.
func Trace(criteria []Criterion, known map[string]bool) []Untraced {
	var out []Untraced
	for _, criterion := range criteria {
		if len(criterion.Requirements) == 0 {
			out = append(out, Untraced{Criterion: criterion.ID})
		}
		for _, key := range criterion.Requirements {
			if !known[key] {
				out = append(out, Untraced{Criterion: criterion.ID, Requirement: key})
			}
		}
	}
	return out
}
