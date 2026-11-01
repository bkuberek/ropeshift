package contract

import (
	"errors"
	"fmt"
	"slices"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// QuestionChecks hold questions and decisions as ledger records, and the search and
// filters that find them.
var QuestionChecks = []Check{
	{"Q1", "questions, decisions, and added categories replay from their events", checkQuestionRecords},
	{"Q2", "search ranks the better match first", checkSearchRanks},
	{"Q3", "search covers decisions, requirements, and contracts", checkSearchKinds},
	{"Q4", "search stays inside one workspace", checkSearchScope},
	{"Q5", "a resolved question and an overturned decision are no candidates", checkSearchLeavesOut},
	{"Q6", "questions filter by category, status, scope, and text", checkQuestionFilters},
	{"Q7", "decisions filter by category, status, scope, and text", checkDecisionFilters},
	{"Q8", "a workspace extends its categories and cannot use an unknown one", checkCategories},
	{"Q9", "a retry of a record returns what the first attempt wrote", checkRecordRetries},
	{"Q10", "only a member or an orchestrator decides", checkDecisionActors},
}

func (r *Rig) question(s ports.Store, text string, change func(*questions.Question)) (questions.Question, error) {
	draft := questions.Question{ID: r.ID("q"), Text: text, Categories: []string{questions.CategoryProduct}}
	if change != nil {
		change(&draft)
	}
	return r.NewOps(s).RecordQuestion(r.Ctx, r.Scope, draft, r.Actor)
}

func (r *Rig) decision(s ports.Store, statement string, change func(*questions.Decision)) (questions.Decision, error) {
	draft := questions.Decision{
		ID: r.ID("d"), Status: questions.DecisionDecided, Statement: statement, Rationale: "It follows.",
		Citations: []string{"PAY-12"}, Scope: "checkout", Reversibility: questions.Reversible,
		Categories: []string{questions.CategoryProduct},
	}
	if change != nil {
		change(&draft)
	}
	return r.NewOps(s).RecordDecision(r.Ctx, r.Scope, draft, r.Actor)
}

// questionBook replays the whole log.
func (r *Rig) questionBook(s ports.Store) (questions.Book, error) {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return questions.Book{}, err
	}
	book, err := questions.Replay(events)
	if err != nil {
		return questions.Book{}, Violatedf("the log does not replay: %v", err)
	}
	return book, nil
}

// CheckQuestions compares question and decision tables with what the log replays to.
func (r *Rig) CheckQuestions(s ports.Store) error {
	book, err := r.questionBook(s)
	if err != nil {
		return err
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Questions().Questions(ports.RecordFilter{})
		if err != nil {
			return err
		}
		if len(rows) != len(book.Questions) {
			return Violatedf("%d questions, the log replays to %d", len(rows), len(book.Questions))
		}
		for _, row := range rows {
			want, ok := book.Questions[row.ID]
			if !ok || want.Status != row.Status || want.ClaimedBy != row.ClaimedBy || want.Rung != row.Rung ||
				want.NormalizedHash != row.NormalizedHash || want.DecisionID != row.DecisionID || want.Blocking != row.Blocking {
				return Violatedf("question %s disagrees with its log: %+v and %+v", row.ID, row, want)
			}
		}
		attachments, err := tx.Questions().Attachments("")
		if err != nil {
			return err
		}
		if len(attachments) != len(book.Attachments) {
			return Violatedf("%d attachments, the log replays to %d", len(attachments), len(book.Attachments))
		}
		for _, row := range attachments {
			if _, ok := book.Attachments[questions.AttachmentKey(row.QuestionID, row.Lane)]; !ok {
				return Violatedf("attachment %+v has no history", row)
			}
		}
		decisions, err := tx.Questions().Decisions(ports.RecordFilter{})
		if err != nil {
			return err
		}
		if len(decisions) != len(book.Decisions) {
			return Violatedf("%d decisions, the log replays to %d", len(decisions), len(book.Decisions))
		}
		for _, row := range decisions {
			if want, ok := book.Decisions[row.ID]; !ok || want.Status != row.Status || want.Statement != row.Statement {
				return Violatedf("decision %s disagrees with its log", row.ID)
			}
		}
		added, err := tx.Questions().Categories()
		if err != nil {
			return err
		}
		if len(added) != len(book.Categories) {
			return Violatedf("%d categories, the log replays to %d", len(added), len(book.Categories))
		}
		return nil
	})
}

func checkQuestionRecords(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	if err := ops.AddCategory(r.Ctx, r.Scope, "pricing", r.Actor); err != nil {
		return err
	}
	asked, err := r.question(s, "What is the retry budget?", func(q *questions.Question) {
		q.Categories, q.Blocking, q.SubjectKind, q.SubjectID = []string{"pricing", questions.CategoryData}, true, "contract", "a-contract"
	})
	if err != nil {
		return err
	}
	if asked.Status != questions.StatusOpen || asked.Rung != 1 || asked.NormalizedHash != questions.Hash("what is the retry budget") {
		return Violatedf("question %+v", asked)
	}
	decided, err := r.decision(s, "The budget is three calls.", func(d *questions.Decision) { d.QuestionID = asked.ID })
	if err != nil {
		return err
	}
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		row, err := tx.Questions().Question(asked.ID)
		if err != nil {
			return err
		}
		if !slices.Equal(row.Categories, []string{"pricing", questions.CategoryData}) || !row.Blocking || row.SubjectID != "a-contract" {
			return Violatedf("question read back as %+v", row)
		}
		read, err := tx.Questions().Decision(decided.ID)
		if err != nil {
			return err
		}
		if read.Rationale != "It follows." || read.DecidedBy != r.Actor.ID || read.Reversibility != questions.Reversible ||
			!slices.Equal(read.Citations, []string{"PAY-12"}) || read.QuestionID != asked.ID || read.Scope != "checkout" {
			return Violatedf("decision read back as %+v", read)
		}
		if _, err := tx.Questions().Question("absent"); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("an absent question: %v", err)
		}
		if _, err := tx.Questions().Decision("absent"); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("an absent decision: %v", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := r.CheckRevisions(s, r.Scope); err != nil {
		return err
	}
	return r.CheckQuestions(s)
}

func hitIDs(hits []ports.SearchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.RecordID)
	}
	return ids
}

func (r *Rig) search(s ports.Store, text string, kinds ...string) ([]ports.SearchHit, error) {
	var hits []ports.SearchHit
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		hits, err = tx.Search(ports.SearchQuery{Text: text, Kinds: kinds})
		return err
	})
	return hits, err
}

// checkSearchRanks holds what both databases agree on: a record with more of the terms, and
// one that repeats a rare term, come before one that barely matches. Fillers keep the terms
// rare, because bm25 gives a term in most documents almost no weight.
func checkSearchRanks(r *Rig) error {
	s := r.Store()
	for i := range 6 {
		if _, err := r.decision(s, fmt.Sprintf("Filler number %d says nothing else.", i), nil); err != nil {
			return err
		}
	}
	weak, err := r.decision(s, "The checkout page stays blue.", nil)
	if err != nil {
		return err
	}
	strong, err := r.decision(s, "The checkout retry budget is three calls per checkout.", nil)
	if err != nil {
		return err
	}
	hits, err := r.search(s, "checkout retry budget", questions.KindDecision)
	if err != nil {
		return err
	}
	if len(hits) != 2 || hits[0].RecordID != strong.ID || hits[1].RecordID != weak.ID {
		return Violatedf("ranked %v, want %s then %s", hitIDs(hits), strong.ID, weak.ID)
	}
	if hits[0].Score <= hits[1].Score {
		return Violatedf("scores %+v do not fall", hits)
	}
	return nil
}

func checkSearchKinds(r *Rig) error {
	s := r.Store()
	decided, err := r.decision(s, "The retry budget is three.", nil)
	if err != nil {
		return err
	}
	asked, err := r.question(s, "Who owns the retry budget?", nil)
	if err != nil {
		return err
	}
	source := fake.NewWorkSource()
	source.Write("requirements.md", []byte(RequirementsText("payments", "PAY-12", "A retry budget bounds every call")))
	source.Write(intakeFile, []byte(ContractText("alpha", "Retry budget", "The retry budget of checkout.")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	hits, err := r.search(s, "retry budget", questions.LookupKinds()...)
	if err != nil {
		return err
	}
	kinds := map[string]string{}
	for _, hit := range hits {
		kinds[hit.Kind] = hit.RecordID
	}
	if len(hits) != 3 || kinds[questions.KindDecision] != decided.ID || kinds[questions.KindRequirement] == "" || kinds[questions.KindContract] == "" {
		return Violatedf("rung 1 read %+v", hits)
	}
	open, err := r.search(s, "retry budget", questions.OpenKinds()...)
	if err != nil {
		return err
	}
	if len(open) != 1 || open[0].RecordID != asked.ID {
		return Violatedf("rung 2 read %+v", open)
	}
	source.Write(intakeFile, []byte(ContractText("alpha", "Something else", "Nothing here now.")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	after, err := r.search(s, "retry budget", questions.KindContract)
	if err != nil {
		return err
	}
	if len(after) != 0 {
		return Violatedf("a contract's old version is still a candidate: %+v", after)
	}
	return nil
}

func checkSearchScope(r *Rig) error {
	s := r.Store()
	other := ledger.WorkspaceID("workspace-other")
	if _, err := s.CreateWorkspace(r.Ctx, ledger.Workspace{ID: other, Name: "B"}, r.Actor); err != nil {
		return err
	}
	draft := questions.Question{ID: r.ID("q"), Text: "What is the retry budget?"}
	if _, err := r.NewOps(s).RecordQuestion(r.Ctx, other, draft, r.Actor); err != nil {
		return err
	}
	hits, err := r.search(s, "retry budget", questions.OpenKinds()...)
	if err != nil {
		return err
	}
	if len(hits) != 0 {
		return Violatedf("another workspace's question came back: %+v", hits)
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Questions().Questions(ports.RecordFilter{Text: "retry budget"})
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			return Violatedf("a filter crossed a workspace: %+v", rows)
		}
		return nil
	})
}

func checkSearchLeavesOut(r *Rig) error {
	s := r.Store()
	asked, err := r.question(s, "What is the retry budget?", nil)
	if err != nil {
		return err
	}
	if err := r.NewOps(s).Dismiss(r.Ctx, r.Scope, asked.ID, r.Actor); err != nil {
		return err
	}
	hits, err := r.search(s, "retry budget", questions.OpenKinds()...)
	if err != nil {
		return err
	}
	if len(hits) != 0 {
		return Violatedf("a dismissed question is a candidate: %+v", hits)
	}
	decided, err := r.decision(s, "The retry budget is three.", nil)
	if err != nil {
		return err
	}
	if hits, err = r.search(s, "retry budget", questions.KindDecision); err != nil {
		return err
	}
	if len(hits) != 1 {
		return Violatedf("candidates %+v", hits)
	}
	if err := r.DB.Damage(r.T, "UPDATE decisions SET status = 'overturned' WHERE id = '"+decided.ID+"'"); err != nil {
		return err
	}
	if hits, err = r.search(s, "retry budget", questions.KindDecision); err != nil {
		return err
	}
	if len(hits) != 0 {
		return Violatedf("an overturned decision is a candidate: %+v", hits)
	}
	return nil
}

func (r *Rig) filterQuestions(s ports.Store, filter ports.RecordFilter) ([]string, error) {
	var ids []string
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Questions().Questions(filter)
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return err
	})
	slices.Sort(ids)
	return ids, err
}

func wantIDs(got []string, want ...string) error {
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return Violatedf("selected %v, want %v", got, want)
	}
	return nil
}

func checkQuestionFilters(r *Rig) error {
	s := r.Store()
	product, err := r.question(s, "How long may the retry budget run?", func(q *questions.Question) { q.SubjectID = "alpha" })
	if err != nil {
		return err
	}
	data, err := r.question(s, "Where does the audit data live?", func(q *questions.Question) {
		q.Categories, q.SubjectID = []string{questions.CategoryData, questions.CategorySecurity}, "beta"
	})
	if err != nil {
		return err
	}
	closed, err := r.question(s, "Is the retry budget per call?", func(q *questions.Question) { q.SubjectID = "beta" })
	if err != nil {
		return err
	}
	if err := r.NewOps(s).Dismiss(r.Ctx, r.Scope, closed.ID, r.Actor); err != nil {
		return err
	}
	tests := []struct {
		filter ports.RecordFilter
		want   []string
	}{
		{ports.RecordFilter{}, []string{product.ID, data.ID, closed.ID}},
		{ports.RecordFilter{Category: questions.CategoryData}, []string{data.ID}},
		{ports.RecordFilter{Category: questions.CategorySecurity}, []string{data.ID}},
		{ports.RecordFilter{Category: questions.CategoryProduct}, []string{product.ID, closed.ID}},
		{ports.RecordFilter{Category: "design"}, nil},
		{ports.RecordFilter{Status: string(questions.StatusDismissed)}, []string{closed.ID}},
		{ports.RecordFilter{Status: string(questions.StatusOpen)}, []string{product.ID, data.ID}},
		{ports.RecordFilter{Scope: "beta"}, []string{data.ID, closed.ID}},
		{ports.RecordFilter{Text: "retry budget"}, []string{product.ID, closed.ID}},
		{ports.RecordFilter{Text: "retry budget", Status: string(questions.StatusOpen)}, []string{product.ID}},
		{ports.RecordFilter{Text: "retry budget", Scope: "beta"}, []string{closed.ID}},
		{ports.RecordFilter{Text: "audit", Category: questions.CategorySecurity}, []string{data.ID}},
		{ports.RecordFilter{Text: "audit", Category: questions.CategoryProduct}, nil},
		{ports.RecordFilter{Text: " ?! "}, nil},
	}
	for _, tt := range tests {
		got, err := r.filterQuestions(s, tt.filter)
		if err != nil {
			return err
		}
		if err := wantIDs(got, tt.want...); err != nil {
			return Violatedf("filter %+v: %v", tt.filter, err)
		}
	}
	return nil
}

func checkDecisionFilters(r *Rig) error {
	s := r.Store()
	first, err := r.decision(s, "The retry budget is three calls.", nil)
	if err != nil {
		return err
	}
	second, err := r.decision(s, "Audit data stays in one region.", func(d *questions.Decision) {
		d.Status, d.Categories, d.Scope = questions.DecisionAssumed, []string{questions.CategoryData}, "storage"
	})
	if err != nil {
		return err
	}
	tests := []struct {
		filter ports.RecordFilter
		want   []string
	}{
		{ports.RecordFilter{}, []string{first.ID, second.ID}},
		{ports.RecordFilter{Category: questions.CategoryData}, []string{second.ID}},
		{ports.RecordFilter{Status: string(questions.DecisionAssumed)}, []string{second.ID}},
		{ports.RecordFilter{Scope: "checkout"}, []string{first.ID}},
		{ports.RecordFilter{Text: "retry"}, []string{first.ID}},
		{ports.RecordFilter{Text: "retry", Scope: "storage"}, nil},
	}
	for _, tt := range tests {
		var got []string
		err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
			rows, err := tx.Questions().Decisions(tt.filter)
			for _, row := range rows {
				got = append(got, row.ID)
			}
			return err
		})
		if err != nil {
			return err
		}
		slices.Sort(got)
		if err := wantIDs(got, tt.want...); err != nil {
			return Violatedf("filter %+v: %v", tt.filter, err)
		}
	}
	return nil
}

func checkCategories(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	if _, err := r.question(s, "A question", func(q *questions.Question) { q.Categories = []string{"pricing"} }); !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("an unknown category: %v", err)
	}
	if err := ops.AddCategory(r.Ctx, r.Scope, "pricing", ledger.Actor{Kind: ledger.ActorLane, ID: "lane"}); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a lane added a category: %v", err)
	}
	for _, name := range []string{"Pricing", "has space", "under_score", "per%cent", ""} {
		if err := ops.AddCategory(r.Ctx, r.Scope, name, r.Actor); !errors.Is(err, questions.ErrInvalid) {
			return Violatedf("category %q: %v", name, err)
		}
	}
	if err := ops.AddCategory(r.Ctx, r.Scope, "pricing", r.Actor); err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if err := ops.AddCategory(r.Ctx, r.Scope, "pricing", r.Actor); err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before {
		return Violatedf("a repeated category moved the revision from %d to %d", before, after)
	}
	if _, err := r.question(s, "A question", func(q *questions.Question) { q.Categories = []string{"pricing"} }); err != nil {
		return Violatedf("an added category: %v", err)
	}
	return r.CheckQuestions(s)
}

func checkRecordRetries(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	draft := questions.Question{ID: r.ID("q"), Text: "What is the retry budget?"}
	first, err := ops.RecordQuestion(r.Ctx, r.Scope, draft, r.Actor)
	if err != nil {
		return err
	}
	draft.Text = "Something else"
	again, err := ops.RecordQuestion(r.Ctx, r.Scope, draft, r.Actor)
	if err != nil {
		return err
	}
	if again.Text != first.Text {
		return Violatedf("a retry replaced the record: %+v", again)
	}
	decision := questions.Decision{ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Three."}
	made, err := ops.RecordDecision(r.Ctx, r.Scope, decision, r.Actor)
	if err != nil {
		return err
	}
	decision.Statement = "Four."
	repeated, err := ops.RecordDecision(r.Ctx, r.Scope, decision, r.Actor)
	if err != nil {
		return err
	}
	if repeated.Statement != made.Statement {
		return Violatedf("a retry replaced the decision: %+v", repeated)
	}
	if _, err := ops.RecordQuestion(r.Ctx, r.Scope, questions.Question{ID: r.ID("q"), Text: " ?! "}, r.Actor); !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("a question with no text: %v", err)
	}
	return r.CheckQuestions(s)
}

func checkDecisionActors(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	for _, kind := range []ledger.ActorKind{ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		draft := questions.Decision{ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Three."}
		if _, err := ops.RecordDecision(r.Ctx, r.Scope, draft, ledger.Actor{Kind: kind, ID: "someone"}); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s decided: %v", kind, err)
		}
	}
	for _, kind := range []ledger.ActorKind{ledger.ActorMember, ledger.ActorOrchestrator} {
		draft := questions.Decision{ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Three."}
		if _, err := ops.RecordDecision(r.Ctx, r.Scope, draft, ledger.Actor{Kind: kind, ID: "someone"}); err != nil {
			return Violatedf("a %s could not decide: %v", kind, err)
		}
	}
	return nil
}
