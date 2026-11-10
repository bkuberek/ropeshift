package contract

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
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
	{"Q11", "ask records a question and returns pending", checkAskPending},
	{"Q12", "the same text attaches a second lane and asks nothing twice", checkAskAttaches},
	{"Q13", "an answered question answers at once, with its citation", checkAskAnswered},
	{"Q14", "a dismissed question or an overturned answer does not match", checkAskPastNoMatch},
	{"Q15", "racing asks of one text make one question", checkAskRace},
	{"Q16", "racing takers have one winner", checkTakeRace},
	{"Q17", "a hold lapses in database time, and a holder keeps its own", checkTakeLapses},
	{"Q18", "only the holder climbs, escalates, or resolves", checkHoldRequired},
	{"Q19", "the gate refuses on the ledger, and names what is missing", checkGateOnLedger},
	{"Q20", "a one-way category skips rungs 3 and 4 and keeps 1 and 2", checkOneWayRungs},
	{"Q21", "an open blocking question holds work in review", checkBlockingHolds},
	{"Q22", "a blocking question sends ready work back to review", checkBlockingReopens},
	{"Q23", "an agent's match merges a question into an open one", checkMerge},
	{"Q24", "a retried ask finds its first attempt", checkAskRetry},
	{"Q25", "a read goes on from where the last one stopped", checkRecordsWalk},
	{"Q26", "a holder lets go of what it holds, and of nothing else, on the log", checkReleaseHeld},
	{"Q27", "only one who may hold a question lets one go", checkReleaseActors},
	{"Q28", "a planner's question reaches the person at once, with no ladder trace", checkDirectReachesPerson},
	{"Q29", "a planner's question needs a category and no options", checkDirectNeedsCategory},
	{"Q30", "no rung can take, climb, decide, assume, escalate, or merge a planner's question", checkDirectRefusesTheLadder},
	{"Q31", "a planner's question matches only what the person owns", checkDirectMatching},
	{"Q32", "a member answers a planner's question, and the answer is a member's decision", checkDirectAnswered},
}

// walk reads every record past the bound of one read, a page at a time. A page that does not
// move on is a violation, because a reader would otherwise never end.
func walk[T any](read func(ports.RecordFilter) ([]T, error), id func(T) string, page int) ([]T, error) {
	var out []T
	after := ""
	for {
		rows, err := read(ports.RecordFilter{Limit: page, After: after})
		if err != nil {
			return nil, err
		}
		if after != "" && len(rows) != 0 && id(rows[0]) <= after {
			return nil, Violatedf("a page after %s starts at %s", after, id(rows[0]))
		}
		out = append(out, rows...)
		if len(rows) < page {
			return out, nil
		}
		after = id(rows[len(rows)-1])
	}
}

func questionID(q questions.Question) string { return q.ID }

func decisionID(d questions.Decision) string { return d.ID }

func checkRecordsWalk(r *Rig) error {
	s := r.Store()
	var asked, decided []string
	for i := range 5 {
		question, err := r.question(s, fmt.Sprintf("Which of these is number %d?", i), nil)
		if err != nil {
			return err
		}
		asked = append(asked, question.ID)
		decision, err := r.decision(s, fmt.Sprintf("Number %d stands.", i), nil)
		if err != nil {
			return err
		}
		decided = append(decided, decision.ID)
	}
	slices.Sort(asked)
	slices.Sort(decided)
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		for _, page := range []int{1, 2, 5} {
			rows, err := walk(tx.Questions().Questions, questionID, page)
			if err != nil {
				return err
			}
			if got := project(rows, questionID); !slices.Equal(got, asked) {
				return Violatedf("pages of %d walked %v, want %v", page, got, asked)
			}
			records, err := walk(tx.Questions().Decisions, decisionID, page)
			if err != nil {
				return err
			}
			if got := project(records, decisionID); !slices.Equal(got, decided) {
				return Violatedf("pages of %d walked %v, want %v", page, got, decided)
			}
		}
		return nil
	})
}

func project[T any](rows []T, id func(T) string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, id(row))
	}
	return out
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

// CheckSessions proves a session and its record commit together: no token can sign a
// member in without the ledger saying so.
func (r *Rig) CheckSessions(s ports.Store) error {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	signedIn := 0
	for _, event := range events {
		if event.Type == control.EventSignedIn {
			signedIn++
		}
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		sessions, err := tx.Members().Sessions()
		if err != nil {
			return err
		}
		if len(sessions) > signedIn {
			return Violatedf("%d sessions, and %d records of a sign-in", len(sessions), signedIn)
		}
		return nil
	})
}

// checkAnswersStand proves an answered question names a decision the ledger holds, so no
// lane can be told a question has an answer nobody can read.
func checkAnswersStand(tx ports.ReadTx, rows []questions.Question) error {
	for _, row := range rows {
		if row.Status != questions.StatusAnswered {
			continue
		}
		if _, err := tx.Questions().Decision(row.DecisionID); err != nil {
			return Violatedf("question %s is answered by %q: %v", row.ID, row.DecisionID, err)
		}
	}
	return nil
}

// checkBatchesReplay compares the batch table with the log, which is what counts
// interruptions, so a batch and its count cannot disagree.
func checkBatchesReplay(tx ports.ReadTx, book questions.Book) error {
	batches, err := tx.Questions().Batches()
	if err != nil {
		return err
	}
	if len(batches) != len(book.Batches) {
		return Violatedf("%d batches, the log replays to %d", len(batches), len(book.Batches))
	}
	for _, batch := range batches {
		if book.Batches[batch.ID] != batch {
			return Violatedf("batch %+v disagrees with its log: %+v", batch, book.Batches[batch.ID])
		}
	}
	return nil
}

// CheckQuestions compares question and decision tables with what the log replays to.
func (r *Rig) CheckQuestions(s ports.Store) error {
	book, err := r.questionBook(s)
	if err != nil {
		return err
	}
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := walk(tx.Questions().Questions, questionID, ports.MaxRecords)
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
		if err := checkAnswersStand(tx, rows); err != nil {
			return err
		}
		if err := checkBatchesReplay(tx, book); err != nil {
			return err
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
		decisions, err := walk(tx.Questions().Decisions, decisionID, ports.MaxRecords)
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
		if err := checkPoliciesReplay(tx, book); err != nil {
			return err
		}
		return checkDecisionsStand(tx, decisions, book)
	})
	if err != nil {
		return err
	}
	return r.CheckExperiences(s)
}

// checkPoliciesReplay compares every policy version with the log, and each with its digest.
func checkPoliciesReplay(tx ports.ReadTx, book questions.Book) error {
	versions, err := tx.Policies().Versions(authority.PolicyDecision)
	if err != nil {
		return err
	}
	lane, err := tx.Policies().Versions(authority.PolicyLane)
	if err != nil {
		return err
	}
	delegation, err := tx.Policies().Versions(authority.PolicyDelegation)
	if err != nil {
		return err
	}
	versions = append(append(versions, lane...), delegation...)
	if len(versions) != len(book.Policies) {
		return Violatedf("%d policy versions, the log replays to %d", len(versions), len(book.Policies))
	}
	for _, version := range versions {
		if book.Policies[questions.PolicyKey(version.Kind, version.Version)] != version {
			return Violatedf("policy version %d disagrees with its log", version.Version)
		}
		if err := version.CheckDigest(); err != nil {
			return Violatedf("%v", err)
		}
	}
	return nil
}

// checkDecisionsStand proves the records around a decision commit with it: its experience
// record, the version it names, and for an overturned one the rest of its five steps.
func checkDecisionsStand(tx ports.ReadTx, rows []questions.Decision, book questions.Book) error {
	notices, err := tx.Questions().Notices("")
	if err != nil {
		return err
	}
	if len(notices) != len(book.Notices) {
		return Violatedf("%d notices, the log replays to %d", len(notices), len(book.Notices))
	}
	successors := map[string]int{}
	for _, row := range rows {
		if row.Supersedes != "" {
			successors[row.Supersedes]++
		}
	}
	for _, row := range rows {
		experience, err := tx.Measures().Experience(measures.SubjectDecision, row.ID)
		if err != nil {
			return Violatedf("decision %s has no experience record: %v", row.ID, err)
		}
		if row.PolicyVersion == 0 || experience.PolicyVersion != row.PolicyVersion {
			return Violatedf("decision %s names version %d, its record %d", row.ID, row.PolicyVersion, experience.PolicyVersion)
		}
		if row.Status != questions.DecisionOverturned {
			if experience.Outcome != nil {
				return Violatedf("decision %s has an outcome and is %s", row.ID, row.Status)
			}
			continue
		}
		if successors[row.ID] != 1 {
			return Violatedf("overturned decision %s has %d successors", row.ID, successors[row.ID])
		}
		logged, ok := book.Outcomes[row.ID]
		if !ok || experience.Outcome == nil || !slices.Equal(logged.ReworkWorkOrders, experience.Outcome.ReworkWorkOrders) {
			return Violatedf("overturned decision %s has outcome %+v, the log %+v", row.ID, experience.Outcome, logged)
		}
		for _, id := range experience.Outcome.ReworkWorkOrders {
			order, err := tx.WorkOrders().Get(id)
			if err != nil {
				return Violatedf("overturned decision %s names rework %s: %v", row.ID, id, err)
			}
			if order.Kind != workorders.KindRework || order.ReworkOf == "" {
				return Violatedf("rework %+v links to nothing", order)
			}
		}
	}
	return nil
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

func (r *Rig) orchestrator(id string) ledger.Actor {
	return ledger.Actor{Kind: ledger.ActorOrchestrator, ID: id}
}

func (r *Rig) laneActor(lane string) ledger.Actor {
	return ledger.Actor{Kind: ledger.ActorLane, ID: lane}
}

func (r *Rig) ask(s ports.Store, text, lane string, change func(*questions.Ask)) (questions.AskResult, error) {
	ask := questions.Ask{
		ID: r.ID("q"), Text: text, Categories: []string{questions.CategoryProduct}, Lane: lane, Generation: 1,
	}
	if change != nil {
		change(&ask)
	}
	return r.NewOps(s).Ask(r.Ctx, r.Scope, ask, r.laneActor(lane))
}

func (r *Rig) attachments(s ports.Store, question string) ([]questions.Attachment, error) {
	var out []questions.Attachment
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Questions().Attachments(question)
		return err
	})
	return out, err
}

func (r *Rig) readQuestion(s ports.Store, id string) (questions.Question, error) {
	var out questions.Question
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Questions().Question(id)
		return err
	})
	return out, err
}

func checkAskPending(r *Rig) error {
	s := r.Store()
	result, err := r.ask(s, "What is the retry budget?", "lane-a", func(a *questions.Ask) { a.Blocking = true })
	if err != nil {
		return err
	}
	if result.Status != questions.AskPending || result.QuestionID == "" {
		return Violatedf("result %+v", result)
	}
	row, err := r.readQuestion(s, result.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusOpen || !row.Blocking || row.AskedBy != "lane-a" {
		return Violatedf("question %+v", row)
	}
	attached, err := r.attachments(s, result.QuestionID)
	if err != nil {
		return err
	}
	if len(attached) != 1 || attached[0].Lane != "lane-a" || attached[0].Generation != 1 || !attached[0].Blocking {
		return Violatedf("attachments %+v", attached)
	}
	if _, err := r.ask(s, " ?! ", "lane-a", nil); !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("an empty question: %v", err)
	}
	if _, err := r.ask(s, "A question", "lane-a", func(a *questions.Ask) { a.Categories = []string{"unknown"} }); !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("an unknown category: %v", err)
	}
	return r.CheckQuestions(s)
}

func checkAskAttaches(r *Rig) error {
	s := r.Store()
	first, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	second, err := r.ask(s, "what is the  RETRY budget", "lane-b", nil)
	if err != nil {
		return err
	}
	if second.Status != questions.AskAttached || second.QuestionID != first.QuestionID {
		return Violatedf("the second ask: %+v", second)
	}
	attached, err := r.attachments(s, first.QuestionID)
	if err != nil {
		return err
	}
	if len(attached) != 2 {
		return Violatedf("attachments %+v", attached)
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	again, err := r.ask(s, "What is the retry budget?", "lane-b", nil)
	if err != nil {
		return err
	}
	if again.Status != questions.AskAttached {
		return Violatedf("a repeat: %+v", again)
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before {
		return Violatedf("a repeated ask moved the revision from %d to %d", before, after)
	}
	rows, err := r.filterQuestions(s, ports.RecordFilter{})
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return Violatedf("the same question was asked %d times", len(rows))
	}
	return r.CheckQuestions(s)
}

// answered takes a question through rung 1: an orchestrator judges that a decision answers it.
func (r *Rig) answered(s ports.Store, text string) (questions.AskResult, questions.Decision, error) {
	ops := r.NewOps(s)
	asked, err := r.ask(s, text, "lane-a", nil)
	if err != nil {
		return questions.AskResult{}, questions.Decision{}, err
	}
	decided, err := r.decision(s, "Three calls.", func(d *questions.Decision) { d.Citations = []string{"PAY-12"} })
	if err != nil {
		return questions.AskResult{}, questions.Decision{}, err
	}
	holder := r.orchestrator("orchestrator-1")
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder); err != nil {
		return questions.AskResult{}, questions.Decision{}, err
	}
	if _, err := ops.Resolve(r.Ctx, r.Scope, asked.QuestionID, decided.ID, holder); err != nil {
		return questions.AskResult{}, questions.Decision{}, err
	}
	return asked, decided, nil
}

func checkAskAnswered(r *Rig) error {
	s := r.Store()
	asked, decided, err := r.answered(s, "What is the retry budget?")
	if err != nil {
		return err
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusAnswered || row.DecisionID != decided.ID || row.ClaimedBy != "" {
		return Violatedf("question %+v", row)
	}
	result, err := r.ask(s, "What is the retry budget?", "lane-b", nil)
	if err != nil {
		return err
	}
	if result.Status != questions.AskAnswered || result.Answer != "Three calls." || result.DecisionID != decided.ID ||
		!slices.Equal(result.Citations, []string{"PAY-12"}) || result.QuestionID != asked.QuestionID {
		return Violatedf("result %+v", result)
	}
	return r.CheckQuestions(s)
}

func checkAskPastNoMatch(r *Rig) error {
	s := r.Store()
	dismissed, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	if err := r.NewOps(s).Dismiss(r.Ctx, r.Scope, dismissed.QuestionID, r.Actor); err != nil {
		return err
	}
	again, err := r.ask(s, "What is the retry budget?", "lane-b", nil)
	if err != nil {
		return err
	}
	if again.Status != questions.AskPending || again.QuestionID == dismissed.QuestionID {
		return Violatedf("a dismissed question matched: %+v", again)
	}
	_, decided, err := r.answered(s, "Where does audit data live?")
	if err != nil {
		return err
	}
	if err := r.DB.Damage(r.T, "UPDATE decisions SET status = 'overturned' WHERE id = '"+decided.ID+"'"); err != nil {
		return err
	}
	fresh, err := r.ask(s, "Where does audit data live?", "lane-b", nil)
	if err != nil {
		return err
	}
	if fresh.Status != questions.AskPending {
		return Violatedf("an overturned answer still answers: %+v", fresh)
	}
	return nil
}

func checkAskRace(r *Rig) error {
	s := r.Store()
	const lanes = 8
	var wg sync.WaitGroup
	results := make([]questions.AskResult, lanes)
	failures := make([]error, lanes)
	for i := range lanes {
		wg.Go(func() {
			results[i], failures[i] = r.ask(s, "What is the retry budget?", fmt.Sprintf("lane-%d", i), nil)
		})
	}
	wg.Wait()
	if err := errors.Join(failures...); err != nil {
		return err
	}
	pending := 0
	for _, result := range results {
		if result.Status == questions.AskPending {
			pending++
		}
		if result.QuestionID != results[0].QuestionID {
			return Violatedf("two questions: %+v", results)
		}
	}
	if pending != 1 {
		return Violatedf("%d lanes asked first: %+v", pending, results)
	}
	attached, err := r.attachments(s, results[0].QuestionID)
	if err != nil {
		return err
	}
	if len(attached) != lanes {
		return Violatedf("%d attachments", len(attached))
	}
	return r.CheckQuestions(s)
}

func checkTakeRace(r *Rig) error {
	s := r.Store()
	asked, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	const takers = 8
	var wg sync.WaitGroup
	failures := make([]error, takers)
	for i := range takers {
		wg.Go(func() {
			_, failures[i] = ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, r.orchestrator(fmt.Sprintf("orchestrator-%d", i)))
		})
	}
	wg.Wait()
	won := 0
	for _, err := range failures {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, questions.ErrTaken):
			return err
		}
	}
	if won != 1 {
		return Violatedf("%d takers won", won)
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusTriage || row.ClaimedBy == "" {
		return Violatedf("question %+v", row)
	}
	return r.CheckQuestions(s)
}

func checkTakeLapses(r *Rig) error {
	s := r.Store()
	asked, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	first, second := r.orchestrator("first"), r.orchestrator("second")
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, first); err != nil {
		return err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, first); err != nil {
		return Violatedf("a holder could not keep its hold: %v", err)
	}
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, second); !errors.Is(err, questions.ErrTaken) {
		return Violatedf("a rival took a live hold: %v", err)
	}
	r.DB.ShiftClock(TTLMs + 1)
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, second); err != nil {
		return Violatedf("a lapsed hold stayed held: %v", err)
	}
	if _, err := ops.Climb(r.Ctx, r.Scope, asked.QuestionID, questions.Step{Rung: questions.RungLookup}, first); !errors.Is(err, questions.ErrNotHeld) {
		return Violatedf("the first holder worked on after it lapsed: %v", err)
	}
	return nil
}

// A session that ended its turn holding a question leaves it for nobody to read. Letting go is what
// a hold's end would do, sooner: anyone may take it at once, and the holder works it no further.
func checkReleaseHeld(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	held, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	rivals, err := r.ask(s, "Which queue takes the retries?", "lane-b", nil)
	if err != nil {
		return err
	}
	first, second := r.orchestrator("first"), r.orchestrator("second")
	taken, err := ops.Take(r.Ctx, r.Scope, held.QuestionID, TTLMs, first)
	if err != nil {
		return err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, rivals.QuestionID, TTLMs, second); err != nil {
		return err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, held.QuestionID, TTLMs, second); !errors.Is(err, questions.ErrTaken) {
		return Violatedf("the baseline: a rival took a live hold: %v", err)
	}
	before, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	released, err := ops.LetGo(r.Ctx, r.Scope, first)
	if err != nil {
		return err
	}
	if !slices.Equal(released, []string{held.QuestionID}) {
		return Violatedf("released %v", released)
	}
	after, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	if len(after) != len(before)+1 || after[len(after)-1].Type != questions.EventQuestionChanged {
		return Violatedf("%d events became %d", len(before), len(after))
	}
	var row questions.Question
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Questions().Question(held.QuestionID)
		return err
	})
	if err != nil {
		return err
	}
	if row.ClaimedBy != "" || row.ClaimUntil >= taken.ClaimUntil {
		return Violatedf("a hold let go of reads %s until %d, from %d", row.ClaimedBy, row.ClaimUntil, taken.ClaimUntil)
	}
	if _, err := ops.Climb(r.Ctx, r.Scope, held.QuestionID, questions.Step{Rung: questions.RungLookup}, first); !errors.Is(err, questions.ErrNotHeld) {
		return Violatedf("a holder worked on after it let go: %v", err)
	}
	if _, err := ops.Take(r.Ctx, r.Scope, rivals.QuestionID, TTLMs, first); !errors.Is(err, questions.ErrTaken) {
		return Violatedf("a release reached another holder's question: %v", err)
	}
	if _, err := ops.Take(r.Ctx, r.Scope, held.QuestionID, TTLMs, second); err != nil {
		return Violatedf("a rival could not take what was let go: %v", err)
	}
	again, err := ops.LetGo(r.Ctx, r.Scope, first)
	if err != nil {
		return err
	}
	if len(again) != 0 {
		return Violatedf("nothing held released %v", again)
	}
	last, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	if len(last) != len(after)+1 {
		return Violatedf("a release of nothing wrote %d events", len(last)-len(after)-1)
	}
	return nil
}

func checkReleaseActors(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	asked, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	if _, err := ops.LetGo(r.Ctx, r.Scope, r.laneActor("lane-a")); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a lane let go of a question: %v", err)
	}
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, r.member()); err != nil {
		return err
	}
	released, err := ops.LetGo(r.Ctx, r.Scope, r.member())
	if err != nil {
		return Violatedf("the baseline: a member could not let go: %v", err)
	}
	if !slices.Equal(released, []string{asked.QuestionID}) {
		return Violatedf("a member released %v", released)
	}
	return nil
}

func checkHoldRequired(r *Rig) error {
	s := r.Store()
	asked, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	stranger := r.orchestrator("stranger")
	if _, err := ops.Climb(r.Ctx, r.Scope, asked.QuestionID, questions.Step{Rung: questions.RungLookup}, stranger); !errors.Is(err, questions.ErrNotHeld) {
		return Violatedf("a climb with no hold: %v", err)
	}
	if _, err := ops.Escalate(r.Ctx, r.Scope, asked.QuestionID, questions.Escalation{}, stranger); !errors.Is(err, questions.ErrNotHeld) {
		return Violatedf("an escalation with no hold: %v", err)
	}
	if _, err := ops.Resolve(r.Ctx, r.Scope, asked.QuestionID, "absent", stranger); !errors.Is(err, questions.ErrNotHeld) {
		return Violatedf("an answer with no hold: %v", err)
	}
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, r.laneActor("lane-a")); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a lane took its own question: %v", err)
	}
	holder := r.orchestrator("holder")
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder); err != nil {
		return err
	}
	if _, err := ops.Climb(r.Ctx, r.Scope, asked.QuestionID, questions.Step{Rung: questions.RungOpen}, holder); !errors.Is(err, questions.ErrWrongRung) {
		return Violatedf("a step past the next rung: %v", err)
	}
	if err := r.NewOps(s).Dismiss(r.Ctx, r.Scope, asked.QuestionID, r.Actor); err != nil {
		return err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder); !errors.Is(err, questions.ErrResolved) {
		return Violatedf("a take of a closed question: %v", err)
	}
	return nil
}

// climbed takes a question to the top of rungs 1 and 2.
func (r *Rig) climbed(s ports.Store, id string, holder ledger.Actor) error {
	ops := r.NewOps(s)
	if _, err := ops.Take(r.Ctx, r.Scope, id, TTLMs, holder); err != nil {
		return err
	}
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		step := questions.Step{Rung: rung, Candidates: []string{"decision:d1"}, Outcome: "no match"}
		if _, err := ops.Climb(r.Ctx, r.Scope, id, step, holder); err != nil {
			return err
		}
	}
	return nil
}

func checkGateOnLedger(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	holder := r.orchestrator("holder")
	asked, err := r.ask(s, "May audit data leave the region?", "lane-a", func(a *questions.Ask) { a.Categories = []string{questions.CategoryData} })
	if err != nil {
		return err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder); err != nil {
		return err
	}
	full := questions.Escalation{Reason: questions.ReasonOneWay, Options: []string{"Keep", "Allow"}, Recommendation: "Keep"}
	_, err = ops.Escalate(r.Ctx, r.Scope, asked.QuestionID, full, holder)
	var gate *questions.GateError
	if !errors.As(err, &gate) || len(gate.Failures) != 1 || gate.Failures[0].Check != questions.GateTrace {
		return Violatedf("an escalation with no trace: %v", err)
	}
	if err := r.climbed(s, asked.QuestionID, holder); err != nil {
		return err
	}
	_, err = ops.Escalate(r.Ctx, r.Scope, asked.QuestionID, questions.Escalation{Reason: "hard"}, holder)
	if !errors.As(err, &gate) || len(gate.Failures) != 3 {
		return Violatedf("an escalation with no reason, options, or recommendation: %v", err)
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.Status == questions.StatusNeedsHuman {
		return Violatedf("a refused escalation reached a human: %+v", row)
	}
	escalated, err := ops.Escalate(r.Ctx, r.Scope, asked.QuestionID, full, holder)
	if err != nil {
		return err
	}
	if escalated.Status != questions.StatusNeedsHuman || escalated.Rung != questions.RungEscalate ||
		escalated.EscalationReason != questions.ReasonOneWay || escalated.Recommendation != "Keep" || len(escalated.Options) != 2 {
		return Violatedf("question %+v", escalated)
	}
	return r.CheckQuestions(s)
}

func checkOneWayRungs(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	holder := r.orchestrator("holder")
	oneWay, err := r.ask(s, "May audit data leave the region?", "lane-a", func(a *questions.Ask) { a.Categories = []string{questions.CategorySecurity} })
	if err != nil {
		return err
	}
	if err := r.climbed(s, oneWay.QuestionID, holder); err != nil {
		return err
	}
	row, err := r.readQuestion(s, oneWay.QuestionID)
	if err != nil {
		return err
	}
	if row.Rung != questions.RungEscalate {
		return Violatedf("a one-way category stands on rung %d", row.Rung)
	}
	if _, err := ops.Climb(r.Ctx, r.Scope, oneWay.QuestionID, questions.Step{Rung: questions.RungDecide}, holder); !errors.Is(err, questions.ErrOneWay) {
		return Violatedf("rung 3 on a one-way category: %v", err)
	}
	other, err := r.ask(s, "What color is the button?", "lane-a", nil)
	if err != nil {
		return err
	}
	if err := r.climbed(s, other.QuestionID, holder); err != nil {
		return err
	}
	row, err = r.readQuestion(s, other.QuestionID)
	if err != nil {
		return err
	}
	if row.Rung != questions.RungDecide {
		return Violatedf("another category stands on rung %d", row.Rung)
	}
	return nil
}

func checkBlockingHolds(r *Rig) error {
	s := r.Store()
	order, err := r.reviewed(s)
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	asked, err := r.ask(s, "Is this the right budget?", "lane-a", func(a *questions.Ask) {
		a.Blocking, a.SubjectKind, a.SubjectID = true, workorders.SubjectKind, order.ID
	})
	if err != nil {
		return err
	}
	result, err := ops.Pass(r.Ctx, r.Scope, order.ID, r.member())
	if err != nil {
		return err
	}
	if result.State != workorders.ContractReview || len(result.Failures) != 1 || result.Failures[0].Check != workorders.CheckQuestions {
		return Violatedf("a review past a blocking question: %+v", result)
	}
	moved, err := ops.Promote(r.Ctx, r.Scope, r.Actor)
	if err != nil {
		return err
	}
	if len(moved) != 0 {
		return Violatedf("the tick moved %v past a blocking question", moved)
	}
	if err := ops.Dismiss(r.Ctx, r.Scope, asked.QuestionID, r.Actor); err != nil {
		return err
	}
	if moved, err = ops.Promote(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if len(moved) != 1 || moved[0] != order.ID {
		return Violatedf("the tick moved %v after the question closed", moved)
	}
	return r.CheckProjection(s)
}

func checkBlockingReopens(r *Rig) error {
	s := r.Store()
	order, _, err := r.passed(s)
	if err != nil {
		return err
	}
	if _, err := r.ask(s, "A question that does not block", "lane-a", func(a *questions.Ask) {
		a.SubjectKind, a.SubjectID = workorders.SubjectKind, order.ID
	}); err != nil {
		return err
	}
	row, err := r.WorkOrder(s, order.ID)
	if err != nil {
		return err
	}
	if row.State != workorders.Ready {
		return Violatedf("a question that blocks nothing moved work: %+v", row)
	}
	if _, err := r.ask(s, "Is this the right contract?", "lane-a", func(a *questions.Ask) {
		a.Blocking, a.SubjectKind, a.SubjectID = true, planning.SubjectContract, order.ContractID
	}); err != nil {
		return err
	}
	if row, err = r.WorkOrder(s, order.ID); err != nil {
		return err
	}
	if row.State != workorders.ContractReview || row.ReviewedBy == "" {
		return Violatedf("a blocking question left %+v", row)
	}
	return r.CheckProjection(s)
}

func checkMerge(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	holder := r.orchestrator("holder")
	target, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	paraphrase, err := r.ask(s, "How many retries may a call make?", "lane-b", func(a *questions.Ask) { a.Blocking = true })
	if err != nil {
		return err
	}
	if err := ops.Merge(r.Ctx, r.Scope, paraphrase.QuestionID, target.QuestionID, holder); !errors.Is(err, questions.ErrNotHeld) {
		return Violatedf("a merge with no hold: %v", err)
	}
	if _, err := ops.Take(r.Ctx, r.Scope, paraphrase.QuestionID, TTLMs, holder); err != nil {
		return err
	}
	if err := ops.Merge(r.Ctx, r.Scope, paraphrase.QuestionID, paraphrase.QuestionID, holder); !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("a merge into itself: %v", err)
	}
	if err := ops.Merge(r.Ctx, r.Scope, paraphrase.QuestionID, target.QuestionID, holder); err != nil {
		return err
	}
	merged, err := r.readQuestion(s, paraphrase.QuestionID)
	if err != nil {
		return err
	}
	if merged.Status != questions.StatusDismissed {
		return Violatedf("merged question %+v", merged)
	}
	into, err := r.readQuestion(s, target.QuestionID)
	if err != nil {
		return err
	}
	if !into.Blocking {
		return Violatedf("a blocking lane joined a question that blocks nothing: %+v", into)
	}
	attached, err := r.attachments(s, target.QuestionID)
	if err != nil {
		return err
	}
	if len(attached) != 2 {
		return Violatedf("attachments %+v", attached)
	}
	return r.CheckQuestions(s)
}

func checkAskRetry(r *Rig) error {
	s := r.Store()
	id := r.ID("q")
	same := func(a *questions.Ask) { a.ID = id }
	first, err := r.ask(s, "What is the retry budget?", "lane-a", same)
	if err != nil {
		return err
	}
	again, err := r.ask(s, "What is the retry budget?", "lane-a", same)
	if err != nil {
		return err
	}
	if again.Status != questions.AskPending || again.QuestionID != first.QuestionID {
		return Violatedf("a retry: %+v", again)
	}
	rows, err := r.filterQuestions(s, ports.RecordFilter{})
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return Violatedf("%d questions", len(rows))
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
		draft := questions.Decision{
			ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Three.", Rationale: "It follows.",
			Citations: []string{"PAY-12"}, Scope: string(kind), Reversibility: questions.Reversible,
		}
		if _, err := ops.RecordDecision(r.Ctx, r.Scope, draft, ledger.Actor{Kind: kind, ID: "someone"}); err != nil {
			return Violatedf("a %s could not decide: %v", kind, err)
		}
	}
	return nil
}

// direct makes an ask the one a planning lane's work order gives, which the control plane sets from
// the ledger and never from the lane.
func direct(a *questions.Ask) { a.Direct = true }

// planner is the lane a direct question comes from.
const planner = "plan-lane"

// waitingItems reads what a member sees of the batches that reached them.
func (r *Rig) waitingItems(s ports.Store) ([]questions.Item, error) {
	var items []questions.Item
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		batches, err := tx.Questions().WaitingBatches()
		if err != nil {
			return err
		}
		for _, batch := range batches {
			rows, err := tx.Questions().BatchQuestions(batch.ID)
			if err != nil {
				return err
			}
			items = append(items, questions.Present(batch, rows, "").Items...)
		}
		return nil
	})
	return items, err
}

func checkDirectReachesPerson(r *Rig) error {
	s := r.Store()
	asked, err := r.ask(s, "Who signs in to the admin console?", planner, direct)
	if err != nil {
		return err
	}
	if asked.Status != questions.AskPending {
		return Violatedf("result %+v", asked)
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusNeedsHuman || row.Rung != questions.RungEscalate || len(row.LadderTrace) != 0 ||
		!row.Direct() || row.BatchID == "" || row.ClaimedBy != "" || len(row.Options) != 0 || row.Recommendation != "" {
		return Violatedf("question %+v", row)
	}
	attached, err := r.attachments(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if len(attached) != 1 || attached[0].Lane != planner {
		return Violatedf("attachments %+v", attached)
	}
	// The window is an hour long and no time passes here, so a delivery is the question hastening its batch.
	if err := r.deliveredOnce(s, asked.QuestionID); err != nil {
		return err
	}
	items, err := r.waitingItems(s)
	if err != nil {
		return err
	}
	if len(items) != 1 || items[0].ID != asked.QuestionID || len(items[0].Options) != 0 {
		return Violatedf("a member sees %+v", items)
	}
	return r.CheckQuestions(s)
}

func checkDirectNeedsCategory(r *Rig) error {
	s := r.Store()
	for name, change := range map[string]func(*questions.Ask){
		"no category":      func(a *questions.Ask) { direct(a); a.Categories = nil },
		"unknown category": func(a *questions.Ask) { direct(a); a.Categories = []string{"unknown"} },
	} {
		if _, err := r.ask(s, "Who signs in to the admin console?", planner, change); !errors.Is(err, questions.ErrInvalid) {
			return Violatedf("%s: %v", name, err)
		}
	}
	rows, err := r.filterQuestions(s, ports.RecordFilter{})
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		return Violatedf("a refused question left rows %v", rows)
	}
	// An ordinary question takes its categories from the orchestrator that escalates it, so it needs none.
	if _, err := r.ask(s, "Which log level do retries use?", "lane-a", func(a *questions.Ask) { a.Categories = nil }); err != nil {
		return Violatedf("an ordinary question with no category: %v", err)
	}
	return nil
}

func checkDirectRefusesTheLadder(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	asked, err := r.ask(s, "Who signs in to the admin console?", planner, direct)
	if err != nil {
		return err
	}
	other, err := r.ask(s, "Which log level do retries use?", "lane-a", nil)
	if err != nil {
		return err
	}
	decided, err := r.decision(s, "Three calls.", nil)
	if err != nil {
		return err
	}
	id, holder := asked.QuestionID, r.orchestrator("holder")
	attempts := []struct {
		name string
		run  func() error
	}{
		{"take", func() error { _, err := ops.Take(r.Ctx, r.Scope, id, TTLMs, holder); return err }},
		{"climb", func() error {
			_, err := ops.Climb(r.Ctx, r.Scope, id, questions.Step{Rung: questions.RungEscalate, Outcome: "no match"}, holder)
			return err
		}},
		{"escalate", func() error {
			_, err := ops.Escalate(r.Ctx, r.Scope, id, questions.Escalation{
				Reason: questions.ReasonScopeChange, Options: []string{"Keep", "Change"}, Recommendation: "Keep",
			}, holder)
			return err
		}},
		{"resolve", func() error { _, err := ops.Resolve(r.Ctx, r.Scope, id, decided.ID, holder); return err }},
		{"decide", func() error {
			_, err := ops.Rule(r.Ctx, r.Scope, id, ruling(r.ID("d"), questions.DecisionDecided), holder)
			return err
		}},
		{"assume", func() error {
			_, err := ops.Rule(r.Ctx, r.Scope, id, ruling(r.ID("d"), questions.DecisionAssumed), holder)
			return err
		}},
		{"merge", func() error { return ops.Merge(r.Ctx, r.Scope, id, other.QuestionID, holder) }},
	}
	for _, attempt := range attempts {
		if err := attempt.run(); !errors.Is(err, questions.ErrDirect) || !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("an orchestrator's %s: %v", attempt.name, err)
		}
	}
	row, err := r.readQuestion(s, id)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusNeedsHuman || row.Rung != questions.RungEscalate || row.ClaimedBy != "" || row.DecisionID != "" || len(row.LadderTrace) != 0 {
		return Violatedf("a refused question changed: %+v", row)
	}
	return r.CheckQuestions(s)
}

func checkDirectMatching(r *Rig) error {
	s := r.Store()
	const text = "Who signs in to the admin console?"
	// An ordinary question on the ladder stays there, because an orchestrator could decide it.
	ordinary, err := r.ask(s, text, "lane-a", nil)
	if err != nil {
		return err
	}
	first, err := r.ask(s, text, planner, direct)
	if err != nil {
		return err
	}
	if first.Status != questions.AskPending || first.QuestionID == ordinary.QuestionID {
		return Violatedf("a planner's question joined the ladder: %+v", first)
	}
	row, err := r.readQuestion(s, ordinary.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusOpen || row.Rung != questions.RungLookup {
		return Violatedf("the ordinary question moved: %+v", row)
	}
	// A planner's question asked again attaches the lane, so the person is asked once.
	again, err := r.ask(s, text, "plan-lane-2", direct)
	if err != nil {
		return err
	}
	if again.Status != questions.AskAttached || again.QuestionID != first.QuestionID {
		return Violatedf("a repeat: %+v", again)
	}
	// And a question from any lane that matches an open one the person owns waits with it.
	person, err := r.ask(s, "Which region holds the audit data?", planner, direct)
	if err != nil {
		return err
	}
	joined, err := r.ask(s, "Which region holds the audit data?", "lane-b", nil)
	if err != nil {
		return err
	}
	if joined.Status != questions.AskAttached || joined.QuestionID != person.QuestionID {
		return Violatedf("an ordinary lane did not join: %+v", joined)
	}
	if err := r.directMatchesAnswers(s); err != nil {
		return err
	}
	return r.CheckQuestions(s)
}

// directMatchesAnswers holds what an answered question gives a planner: a decision answers it, and an
// orchestrator's assumption does not, because no orchestrator may stand in for the person.
func (r *Rig) directMatchesAnswers(s ports.Store) error {
	asked, decided, err := r.answered(s, "What is the retry budget?")
	if err != nil {
		return err
	}
	reused, err := r.ask(s, "What is the retry budget?", planner, direct)
	if err != nil {
		return err
	}
	if reused.Status != questions.AskAnswered || reused.DecisionID != decided.ID || reused.QuestionID != asked.QuestionID {
		return Violatedf("a decided answer was not reused: %+v", reused)
	}
	assumed, err := r.Assumed(s, "Which log level do retries use?", "")
	if err != nil {
		return err
	}
	ordinary, err := r.ask(s, "Which log level do retries use?", "lane-c", nil)
	if err != nil {
		return err
	}
	if ordinary.Status != questions.AskAnswered || ordinary.DecisionID != assumed.ID {
		return Violatedf("an ordinary lane does not read the assumption: %+v", ordinary)
	}
	planned, err := r.ask(s, "Which log level do retries use?", planner, direct)
	if err != nil {
		return err
	}
	if planned.Status != questions.AskPending || planned.QuestionID == assumed.QuestionID {
		return Violatedf("an assumption answered a planner: %+v", planned)
	}
	row, err := r.readQuestion(s, planned.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusNeedsHuman || !row.Direct() {
		return Violatedf("question %+v", row)
	}
	return nil
}

func checkDirectAnswered(r *Rig) error {
	s := r.Store()
	const text = "Who signs in to the admin console?"
	asked, err := r.ask(s, text, planner, direct)
	if err != nil {
		return err
	}
	if err := r.deliveredOnce(s, asked.QuestionID); err != nil {
		return err
	}
	decision, err := r.answer(s, asked.QuestionID, "Only members of the operations group.")
	if err != nil {
		return err
	}
	if decision.Status != questions.DecisionDecided || decision.DecidedBy != r.Actor.ID || decision.QuestionID != asked.QuestionID {
		return Violatedf("decision %+v", decision)
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusAnswered || row.DecisionID != decision.ID || len(row.LadderTrace) != 0 {
		return Violatedf("question after the answer %+v", row)
	}
	items, err := r.waitingItems(s)
	if err != nil {
		return err
	}
	if len(items) != 0 {
		return Violatedf("an answered question still waits: %+v", items)
	}
	again, err := r.ask(s, text, planner, direct)
	if err != nil {
		return err
	}
	if again.Status != questions.AskAnswered || again.DecisionID != decision.ID {
		return Violatedf("the next ask reads %+v", again)
	}
	return r.CheckQuestions(s)
}
