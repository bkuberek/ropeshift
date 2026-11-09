package contract

import (
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// DocumentPackChecks hold what a lane's pack and its notices say of the documents its contract cites
// (FR-DOC-4, FR-DOC-6).
var DocumentPackChecks = []Check{
	{"DP1", "a pack holds each cited document at its pinned version, between the contract and the requirements", checkPackHoldsCitedDocuments},
	{"DP2", "a lane reads a document by version, and a bare ID reads the newest", checkContextReadsDocuments},
	{"DP3", "a new version of a cited document tells the lanes whose packs hold it, and moves no pin", checkNoticeDocument},
	{"DP4", "a new version of a cited document parks a lane the policy says to", checkNoticeDocumentParks},
	{"DP5", "a document no pack holds tells no lane", checkNoticeDocumentNotHeld},
}

// citedAlpha is a contract that cites a spec and an architecture document, and names requirements at
// its own level and in its criterion.
func citedAlpha(statement string) string {
	return `+++
kind = "contract"
id = "alpha"
title = "The contract alpha"
plan = "` + documentsPlan + `"
documents = ["spec:checkout-api", "architecture:payments"]
requirements = ["PAY-1"]

[[criteria]]
id = "c1"
text = "It works"
requirements = ["PAY-2", "PAY-1"]

[[work_orders]]
id = "alpha-order"
title = "The work order of alpha"
+++

` + statement + "\n"
}

// documentedLane is a running lane under a contract that cites documents.
func documentedLane(r *Rig) (*statusRig, *packRig, error) {
	s := r.Store()
	p := &packRig{r: r, s: s, source: fake.NewWorkSource()}
	p.source.Write("plan.md", []byte(PlanText(documentsPlan, "Payments v2", "")))
	p.source.Write("req-one.md", []byte(RequirementsText("one", "PAY-1", "The first requirement.")))
	p.source.Write("req-two.md", []byte(RequirementsText("two", "PAY-2", "The second requirement.")))
	p.source.Write(specFile, []byte(DocumentText("spec", "checkout-api", "Checkout API", documentsPlan, "The calls.")))
	p.source.Write("architecture.md", []byte(DocumentText("architecture", "payments", "Payments", documentsPlan, "The services.")))
	p.source.Write("elsewhere.md", []byte(DocumentText("spec", "elsewhere", "Elsewhere", documentsPlan, "Nobody cites this.")))
	p.source.Write(intakeFile, []byte(citedAlpha("One.")))
	status, err := newStatusRigOn(r, s, func(store ports.Store) (string, error) {
		if _, err := r.intake(store, p.source); err != nil {
			return "", err
		}
		var err error
		if p.order, err = r.orderByTitle(store, "The work order of alpha"); err != nil {
			return "", err
		}
		_, err = r.NewOps(store).Pass(r.Ctx, r.Scope, p.order.ID, r.member())
		return p.order.ID, err
	})
	return status, p, err
}

// changeSpec records a new version of the cited spec.
func (p *packRig) changeSpec(body string) error {
	p.source.Write(specFile, []byte(DocumentText("spec", "checkout-api", "Checkout API", documentsPlan, body)))
	_, err := p.r.intake(p.s, p.source)
	return err
}

func checkPackHoldsCitedDocuments(r *Rig) error {
	status, p, err := documentedLane(r)
	if err != nil {
		return err
	}
	pack, err := status.newestPack()
	if err != nil {
		return err
	}
	want := []string{
		"contract " + planning.ContractKey(p.order.ContractID, 1),
		"document spec:checkout-api@1",
		"document architecture:payments@1",
		"requirement PAY-1",
		"requirement PAY-2",
	}
	if got := itemRefs(pack); !slices.Equal(got, want) {
		return Violatedf("the pack holds %v, want %v", got, want)
	}
	for _, text := range []string{"The calls.", "The services.", "## document spec:checkout-api@1: Checkout API"} {
		if !strings.Contains(pack.Text, text) {
			return Violatedf("the pack does not say %q: %s", text, pack.Text)
		}
	}
	// The spec moves on, and the pack keeps the version its contract cited, text and all.
	if err := p.changeSpec("The calls, changed."); err != nil {
		return err
	}
	pack, err = status.newestPack()
	if err != nil {
		return err
	}
	if pack.Version != 1 || !strings.Contains(pack.Text, "The calls.") || strings.Contains(pack.Text, "changed") {
		return Violatedf("the pack moved with the file: version %d: %s", pack.Version, pack.Text)
	}
	return r.CheckPacks(status.store)
}

func checkContextReadsDocuments(r *Rig) error {
	status, p, err := documentedLane(r)
	if err != nil {
		return err
	}
	if err := p.changeSpec("The calls, changed."); err != nil {
		return err
	}
	for _, tt := range []struct{ ref, found, want string }{
		{"spec:checkout-api@1", "spec:checkout-api@1", "The calls.\n"},
		{"spec:checkout-api@2", "spec:checkout-api@2", "The calls, changed."},
		{"spec:checkout-api", "spec:checkout-api@2", "The calls, changed."},
		{"architecture:payments@1", "architecture:payments@1", "The services."},
	} {
		found, err := status.lookup(contextpacks.KindDocument, tt.ref)
		if err != nil {
			return err
		}
		if err := oneRecord(found, contextpacks.KindDocument, tt.found, tt.want); err != nil {
			return err
		}
	}
	for _, ref := range []string{"spec:checkout-api@9", "spec:unknown", "spec:checkout-api@0", "nonsense"} {
		found, err := status.lookup(contextpacks.KindDocument, ref)
		if err != nil || len(found.Records) != 0 {
			return Violatedf("a read of %q found %+v: %v", ref, found, err)
		}
	}
	return nil
}

func checkNoticeDocument(r *Rig) error {
	status, p, err := documentedLane(r)
	if err != nil {
		return err
	}
	if err := p.changeSpec("The calls, changed."); err != nil {
		return err
	}
	if err := expectNotice(status, messages.KindDocumentNotice, "spec:checkout-api@2"); err != nil {
		return err
	}
	row, err := r.WorkOrder(p.s, p.order.ID)
	if err != nil {
		return err
	}
	if row.ContractVersion != 1 {
		return Violatedf("a running lane's work moved: %+v", row)
	}
	wanted, err := status.wanted()
	if err != nil {
		return err
	}
	if wanted != "" {
		return Violatedf("the default policy asked %q", wanted)
	}
	pack, err := status.newestPack()
	if err != nil {
		return err
	}
	if pack.Version != 1 {
		return Violatedf("a pinned document took pack version %d", pack.Version)
	}
	contracts, err := r.readContracts(p.s)
	if err != nil || len(contracts) != 1 || contracts[0].Documents[0].Version != 1 {
		return Violatedf("contracts %+v moved with the document: %v", contracts, err)
	}
	if err := p.changeSpec("The calls, changed again."); err != nil {
		return err
	}
	notices, err := noticesOf(status, messages.KindDocumentNotice)
	if err != nil || len(notices) != 2 {
		return Violatedf("notices %+v: %v", notices, err)
	}
	return r.CheckPacks(status.store)
}

func checkNoticeDocumentParks(r *Rig) error {
	status, p, err := documentedLane(r)
	if err != nil {
		return err
	}
	if err := publishLanePolicy(status, 2, authority.LanePark, authority.LaneNotify); err != nil {
		return err
	}
	if err := p.changeSpec("The calls, changed."); err != nil {
		return err
	}
	wanted, err := status.wanted()
	if err != nil {
		return err
	}
	if wanted == "" || !strings.Contains(wanted, "spec:checkout-api@2") {
		return Violatedf("the policy parks on a new version, and desired state asks %q", wanted)
	}
	return nil
}

func checkNoticeDocumentNotHeld(r *Rig) error {
	status, p, err := documentedLane(r)
	if err != nil {
		return err
	}
	if err := publishLanePolicy(status, 2, authority.LanePark, authority.LaneNotify); err != nil {
		return err
	}
	p.source.Write("elsewhere.md", []byte(DocumentText("spec", "elsewhere", "Elsewhere", documentsPlan, "Changed, and nobody cites it.")))
	if _, err := r.intake(p.s, p.source); err != nil {
		return err
	}
	notices, err := noticesOf(status, messages.KindDocumentNotice)
	if err != nil {
		return err
	}
	wanted, err := status.wanted()
	if err != nil {
		return err
	}
	if len(notices) != 0 || wanted != "" {
		return Violatedf("notices %+v, wanted %q", notices, wanted)
	}
	return nil
}
