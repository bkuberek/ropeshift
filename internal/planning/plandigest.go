package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Entry is one record a plan version rests on: the plan's file, a document, or a contract. Ref names
// it the way its file does, as `<kind>:<id>`, so the same record has the same ref in a ledger and in a
// commit nobody has read yet. Digest is the digest of its file. Record is the record's ID in the
// ledger, which a contract's ref does not give, and Parts are what a contract holds, which a
// difference reads. Neither Record nor Parts is part of the plan's digest.
type Entry struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
	Record string `json:"record,omitempty"`
	Parts  []Part `json:"parts,omitempty"`
}

// Part is one criterion or one work order of a contract, with the digest of what it says.
type Part struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

// The kinds of ref that are not a document's.
const (
	refPlan     = "plan"
	refContract = "contract"
)

// PlanRef is the ref of a plan's own file, by the ID the file gives itself.
func PlanRef(id string) string { return refPlan + ":" + id }

// ContractRef is the ref of a contract's file, by the ID the file gives itself.
func ContractRef(id string) string { return refContract + ":" + id }

// EntryKind is the kind of record a ref names: `plan`, `contract`, or a document kind.
func EntryKind(ref string) string {
	kind, _, _ := strings.Cut(ref, ":")
	return kind
}

// PlanDigest is the digest of one plan version: a hash over the sorted list of the ref and the digest
// of every record that names the plan, the plan's own file among them. The draft decisions are in the
// plan's file, so its digest covers them. Nothing else about an entry is read, so the same records
// have the same digest wherever they are held and in whatever order they are listed.
func PlanDigest(entries []Entry) string {
	sorted := slices.Clone(entries)
	sort.SliceStable(sorted, func(a, b int) bool {
		if sorted[a].Ref != sorted[b].Ref {
			return sorted[a].Ref < sorted[b].Ref
		}
		return sorted[a].Digest < sorted[b].Digest
	})
	hash := sha256.New()
	for _, entry := range sorted {
		// A ref and a digest never hold a zero byte or a newline, so each boundary is a fact of the list
		// and not of the text.
		hash.Write([]byte(entry.Ref + "\x00" + entry.Digest + "\n"))
	}
	return DigestSHA256 + hex.EncodeToString(hash.Sum(nil))
}

// Covers says an entry holds this record of the ledger at this digest, which is how a contract
// version is covered by a plan version.
func Covers(entries []Entry, record, digest string) bool {
	for _, entry := range entries {
		if entry.Record == record && entry.Digest == digest && EntryKind(entry.Ref) == refContract {
			return true
		}
	}
	return false
}

// CountEntries counts the records of a plan version by kind. The plan's own file is not counted.
func CountEntries(entries []Entry) map[string]int {
	counts := map[string]int{}
	for _, entry := range entries {
		if kind := EntryKind(entry.Ref); kind != refPlan {
			counts[kind]++
		}
	}
	return counts
}

// CriterionPart is the part a criterion makes of its contract.
func CriterionPart(c Criterion) Part {
	requirements := slices.Clone(c.Requirements)
	slices.Sort(requirements)
	return Part{Ref: "criterion:" + c.ID, Digest: ContentDigest("criterion", c.ID, c.Text, strings.Join(requirements, ","))}
}

// WorkOrderPart is the part a work order makes of its contract. It reads what a person sees of the
// work: its title, priority, size, and needs.
func WorkOrderPart(w WorkOrderSpec) Part {
	needs := make([]string, 0, len(w.Needs))
	for key, value := range w.Needs {
		needs = append(needs, key+"="+value)
	}
	slices.Sort(needs)
	read := []string{"work_order", w.ID, w.Title, strconv.FormatInt(w.Priority, 10), w.Size, strings.Join(needs, ",")}
	// A work order that names no base, and continues no branch, keeps the digest it had before the format
	// could name either.
	if w.Base != "" {
		read = append(read, "base="+w.Base)
	}
	if w.Continues != "" {
		read = append(read, "continues="+w.Continues)
	}
	return Part{Ref: "work_order:" + w.ID, Digest: ContentDigest(read...)}
}

// PartDifference lists the parts of one kind that a contract gained, changed, and lost, by ID.
type PartDifference struct {
	Added   []string `json:"added,omitempty"`
	Changed []string `json:"changed,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

// Empty says nothing of this kind differs.
func (d PartDifference) Empty() bool { return len(d.Added)+len(d.Changed)+len(d.Removed) == 0 }

// ContractDifference is what changed inside one contract that both versions list.
type ContractDifference struct {
	Ref        string         `json:"ref"`
	Criteria   PartDifference `json:"criteria"`
	WorkOrders PartDifference `json:"work_orders"`
}

// Difference is how a plan version differs from the last version a person approved. Records are named
// by ref, and a contract that changed also says which criteria and work orders did.
type Difference struct {
	// Base is the approved version this one is compared with, and zero when none has been approved.
	Base      int64                `json:"base,omitempty"`
	Added     []string             `json:"added,omitempty"`
	Changed   []string             `json:"changed,omitempty"`
	Removed   []string             `json:"removed,omitempty"`
	Contracts []ContractDifference `json:"contracts,omitempty"`
}

// Empty says the version differs from its base in no record.
func (d Difference) Empty() bool { return len(d.Added)+len(d.Changed)+len(d.Removed) == 0 }

// Compare reads a version's records against those of the approved version it is compared with.
func Compare(base int64, approved, next []Entry) Difference {
	was := indexEntries(approved)
	now := indexEntries(next)
	difference := Difference{Base: base}
	for _, ref := range sortedRefs(now) {
		before, held := was[ref]
		switch {
		case !held:
			difference.Added = append(difference.Added, ref)
		case before.Digest != now[ref].Digest:
			difference.Changed = append(difference.Changed, ref)
			if EntryKind(ref) != refContract {
				continue
			}
			criteria := comparePartKind(before.Parts, now[ref].Parts, "criterion:")
			orders := comparePartKind(before.Parts, now[ref].Parts, "work_order:")
			if !criteria.Empty() || !orders.Empty() {
				difference.Contracts = append(difference.Contracts, ContractDifference{Ref: ref, Criteria: criteria, WorkOrders: orders})
			}
		}
	}
	for _, ref := range sortedRefs(was) {
		if _, held := now[ref]; !held {
			difference.Removed = append(difference.Removed, ref)
		}
	}
	return difference
}

func indexEntries(entries []Entry) map[string]Entry {
	out := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		out[entry.Ref] = entry
	}
	return out
}

func sortedRefs(entries map[string]Entry) []string {
	refs := make([]string, 0, len(entries))
	for ref := range entries {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	return refs
}

// comparePartKind compares the parts whose ref starts with a prefix, and names them by the ID after it.
func comparePartKind(was, now []Part, prefix string) PartDifference {
	before := map[string]string{}
	for _, part := range was {
		if id, ok := strings.CutPrefix(part.Ref, prefix); ok {
			before[id] = part.Digest
		}
	}
	after := map[string]string{}
	for _, part := range now {
		if id, ok := strings.CutPrefix(part.Ref, prefix); ok {
			after[id] = part.Digest
		}
	}
	var out PartDifference
	for _, id := range sortedKeys(after) {
		digest, held := before[id]
		switch {
		case !held:
			out.Added = append(out.Added, id)
		case digest != after[id]:
			out.Changed = append(out.Changed, id)
		}
	}
	for _, id := range sortedKeys(before) {
		if _, held := after[id]; !held {
			out.Removed = append(out.Removed, id)
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// Summary says in a line how a version differs from the last approved one.
func (d Difference) Summary() string {
	if d.Base == 0 {
		return fmt.Sprintf("a first version with %d records", len(d.Added))
	}
	if d.Empty() {
		return fmt.Sprintf("no record differs from version %d", d.Base)
	}
	return fmt.Sprintf("%d added, %d changed, %d removed since version %d", len(d.Added), len(d.Changed), len(d.Removed), d.Base)
}

// PlanDetail is what a member reads of a plan version in the request to approve it: what the plan is
// called, what it holds, how many answers it drafts, and how it differs from the last approved version.
type PlanDetail struct {
	Title string `json:"title"`
	// Ref is the plan's own ID in its file, which names it before the ledger holds it.
	Ref        string         `json:"ref,omitempty"`
	Counts     map[string]int `json:"counts,omitempty"`
	Decisions  int            `json:"decisions,omitempty"`
	Difference Difference     `json:"difference"`
	Source     string         `json:"source,omitempty"`
}
