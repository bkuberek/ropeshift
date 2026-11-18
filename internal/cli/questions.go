package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/questions"
)

// filterFlags adds the flags a list of questions or decisions shares.
func filterFlags(set *flag.FlagSet, filter *api.RecordFilter) {
	set.StringVar(&filter.Category, "category", "", "only this category")
	set.StringVar(&filter.Status, "status", "", "only this status")
	set.StringVar(&filter.Scope, "scope", "", "only this scope")
	set.StringVar(&filter.Text, "text", "", "only records that match these words, best first")
}

// questionLine names the scope an answer to a question takes with the title of the record it names, when the read
// gave one.
func questionLine(q api.QuestionView) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\n", q.ID, q.Status, strings.Join(q.Categories, ","), api.ScopeLabel(q.Scope, q.ScopeTitle), q.Text)
}

// decisionLine names a decision's scope with the title of the record it names, when the read gave one.
func decisionLine(d api.DecisionView) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s\n", d.ID, d.Status, api.ScopeLabel(d.Scope, d.ScopeTitle), d.Statement)
}

func (c *invocation) questionList(args []string) int {
	set := c.flags()
	var filter api.RecordFilter
	filterFlags(set, &filter)
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	rows, err := client.Questions(c.ctx, filter)
	if rows == nil {
		rows = []api.QuestionView{}
	}
	return c.finish(err, api.QuestionList{Questions: rows}, func() string {
		var out strings.Builder
		for _, row := range rows {
			out.WriteString(questionLine(row))
		}
		return out.String()
	})
}

func (c *invocation) questionShow(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one question ID")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.Question(c.ctx, positional[0])
	return c.finish(err, row, func() string { return questionLine(row) })
}

func (c *invocation) questionRecord(args []string) int {
	set := c.flags()
	var request api.RecordQuestion
	var categories repeated
	set.StringVar(&request.ID, "id", "", "a client-generated ID")
	set.StringVar(&request.Text, "text", "", "the question")
	set.Var(&categories, "category", "a category, repeatable")
	set.StringVar(&request.SubjectID, "subject", "", "the ID of what it is about")
	set.StringVar(&request.SubjectKind, "subject-kind", "", "work_order, contract, or plan")
	set.BoolVar(&request.Blocking, "blocking", false, "work on the subject waits for the answer")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if request.Text == "" {
		return c.usageError("--text is required")
	}
	if request.ID == "" {
		id, err := newID()
		if err != nil {
			return c.failure(err)
		}
		request.ID = id
	}
	request.Categories = categories
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.RecordQuestion(c.ctx, request)
	return c.finish(err, row, func() string {
		return questionLine(api.QuestionView{Question: row, Scope: questions.ScopeOf(row.SubjectKind, row.SubjectID)})
	})
}

func (c *invocation) questionDismiss(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one question ID")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	err := client.DismissQuestion(c.ctx, positional[0])
	return c.finish(err, struct {
		Dismissed string `json:"dismissed"`
	}{positional[0]}, func() string { return positional[0] + "\tdismissed\n" })
}

func (c *invocation) questionAnswer(args []string) int {
	set := c.flags()
	var request api.AnswerQuestion
	var citations, noConflict repeated
	set.StringVar(&request.ID, "id", "", "a client-generated ID for the decision it becomes")
	set.StringVar(&request.Answer, "answer", "", "the answer")
	set.StringVar(&request.Rationale, "rationale", "", "why")
	set.Var(&citations, "citation", "what it rests on, repeatable")
	set.StringVar(&request.Supersedes, "supersedes", "", "the decision in force it replaces, with no rework")
	set.Var(&noConflict, "no-conflict", "a decision its scope overlaps and it does not conflict with, repeatable")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one question ID")
	}
	if request.Answer == "" {
		return c.usageError("--answer is required")
	}
	if request.ID == "" {
		id, err := newID()
		if err != nil {
			return c.failure(err)
		}
		request.ID = id
	}
	request.Citations, request.NoConflict = citations, noConflict
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.AnswerQuestion(c.ctx, positional[0], request)
	return c.finish(err, row, func() string { return decisionLine(api.DecisionView{Decision: row}) })
}

// batchResult is what answering a batch printed: the decisions recorded, and the items it left for the member.
type batchResult struct {
	Decisions  []questions.Decision `json:"decisions"`
	Unanswered []unansweredItem     `json:"unanswered,omitempty"`
}

// unansweredItem is a question of the batch the request did not answer, with why.
type unansweredItem struct {
	Question string `json:"question"`
	Reason   string `json:"reason"`
}

// readBatchAnswers reads a file of answers: a JSON array of the items the batch route takes.
func (c *invocation) readBatchAnswers(path string) ([]api.BatchAnswerItem, error) {
	raw, err := c.readLocal(path)
	if err != nil {
		return nil, err
	}
	var items []api.BatchAnswerItem
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&items); err != nil {
		return nil, fmt.Errorf("%s is not a JSON array of answers: %w", path, err)
	}
	for i := range items {
		if items[i].Question == "" {
			return nil, fmt.Errorf("answer %d of %s names no question", i+1, path)
		}
		if items[i].ID != "" {
			continue
		}
		id, err := newID()
		if err != nil {
			return nil, err
		}
		items[i].ID = id
	}
	return items, nil
}

// remaining adds the recommendation of every reversible item the file does not answer, when the member asked for
// that, and lists each item the request leaves unanswered with why. A one-way item waits for its own choice.
func remaining(batch api.QuestionBatch, given []api.BatchAnswerItem, accept bool) ([]api.BatchAnswerItem, []unansweredItem, error) {
	named := map[string]bool{}
	for _, item := range given {
		named[item.Question] = true
	}
	answers := slices.Clone(given)
	var left []unansweredItem
	for _, item := range batch.Items {
		switch {
		case named[item.ID]:
		case item.Reversibility == questions.OneWay:
			left = append(left, unansweredItem{item.ID, "one-way, so it waits for its own choice"})
		case !accept:
			left = append(left, unansweredItem{item.ID, "not in the file"})
		case item.Recommendation == "":
			left = append(left, unansweredItem{item.ID, "it has no recommendation to accept"})
		default:
			id, err := newID()
			if err != nil {
				return nil, nil, err
			}
			answers = append(answers, api.BatchAnswerItem{Question: item.ID, ID: id, Accepted: true})
		}
	}
	return answers, left, nil
}

func (c *invocation) questionAnswerBatch(args []string) int {
	set := c.flags()
	accept := set.Bool("accept-reversible", false, "accept the recommendation of every reversible item the file does not answer")
	file := set.String("answers", "", "a JSON file holding an array of answers: question, id, answer, rationale, citations, supersedes, no_conflict, accepted")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one batch ID")
	}
	if !*accept && *file == "" {
		return c.usageError("name what to answer: --accept-reversible, --answers FILE, or both")
	}
	var given []api.BatchAnswerItem
	if *file != "" {
		var err error
		if given, err = c.readBatchAnswers(*file); err != nil {
			return c.usageError("%v", err)
		}
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	batches, err := client.Batches(c.ctx, "")
	if err != nil {
		return c.failure(err)
	}
	batch, found := api.QuestionBatch{}, false
	for _, waiting := range batches {
		if waiting.Batch.ID == positional[0] {
			batch, found = waiting, true
		}
	}
	if !found && *accept {
		return c.failure(&api.Error{Status: http.StatusNotFound, Detail: api.ErrorDetail{Code: api.CodeNotFound, Message: fmt.Sprintf("no batch %q waits for a member", positional[0])}})
	}
	answers, left, err := remaining(batch, given, *accept)
	if err != nil {
		return c.failure(err)
	}
	if len(answers) == 0 {
		return c.usageError("nothing to answer: %s", describeUnanswered(left))
	}
	decisions, err := client.AnswerBatch(c.ctx, positional[0], api.AnswerBatch{Answers: answers})
	return c.finish(err, batchResult{Decisions: decisions, Unanswered: left}, func() string {
		var out strings.Builder
		for _, decision := range decisions {
			out.WriteString(decisionLine(api.DecisionView{Decision: decision}))
		}
		for _, item := range left {
			fmt.Fprintf(&out, "%s\tleft unanswered: %s\n", item.Question, item.Reason)
		}
		return out.String()
	})
}

// describeUnanswered says which items a request left, for a request that answered none.
func describeUnanswered(left []unansweredItem) string {
	if len(left) == 0 {
		return "the batch holds no question that waits"
	}
	parts := make([]string, 0, len(left))
	for _, item := range left {
		parts = append(parts, item.Question+" ("+item.Reason+")")
	}
	return strings.Join(parts, ", ")
}

func (c *invocation) questionBatches(args []string) int {
	set := c.flags()
	category := set.String("category", "", "only this category")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	rows, err := client.Batches(c.ctx, *category)
	if rows == nil {
		rows = []api.QuestionBatch{}
	}
	return c.finish(err, api.BatchList{Batches: rows}, func() string {
		var out strings.Builder
		for _, row := range rows {
			fmt.Fprintf(&out, "batch %s\n", row.Batch.ID)
			for _, item := range row.Items {
				out.WriteString(itemLines(item))
			}
		}
		return out.String()
	})
}

// itemLines shows one question with all a member needs to answer it once.
func itemLines(item api.QuestionItem) string {
	var out strings.Builder
	blocking := ""
	if item.Blocking {
		blocking = "\tblocking"
	}
	fmt.Fprintf(&out, "  %s\t%s\t%s%s\n", item.ID, strings.Join(item.Categories, ","), item.Text, blocking)
	fmt.Fprintf(&out, "    options: %s\n    recommendation: %s\n", strings.Join(item.Options, " | "), item.Recommendation)
	if item.RecommendationRationale != "" {
		fmt.Fprintf(&out, "    because: %s\n", item.RecommendationRationale)
	}
	if item.Reversibility != "" {
		fmt.Fprintf(&out, "    the answer is: %s\n", item.Reversibility)
	}
	if item.Scope != "" {
		fmt.Fprintf(&out, "    scope: %s\n", api.ScopeLabel(item.Scope, item.ScopeTitle))
	}
	if len(item.Citations) != 0 {
		fmt.Fprintf(&out, "    citations: %s\n", strings.Join(item.Citations, ", "))
	}
	return out.String()
}

func (c *invocation) decisionList(args []string) int {
	set := c.flags()
	var filter api.RecordFilter
	filterFlags(set, &filter)
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	rows, err := client.Decisions(c.ctx, filter)
	if rows == nil {
		rows = []api.DecisionView{}
	}
	return c.finish(err, api.DecisionList{Decisions: rows}, func() string {
		var out strings.Builder
		for _, row := range rows {
			out.WriteString(decisionLine(row))
		}
		return out.String()
	})
}

func (c *invocation) decisionShow(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one decision ID")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.Decision(c.ctx, positional[0])
	return c.finish(err, row, func() string { return decisionLine(row) })
}

func (c *invocation) decisionRecord(args []string) int {
	set := c.flags()
	var request api.RecordDecision
	var categories, citations, noConflict repeated
	assumed := set.Bool("assumed", false, "record a reversible assumption, not a decision")
	set.StringVar(&request.ID, "id", "", "a client-generated ID")
	set.StringVar(&request.Statement, "statement", "", "what was decided")
	set.StringVar(&request.Rationale, "rationale", "", "why")
	set.StringVar(&request.Scope, "scope", "", "what it binds: plan:<id>, contract:<id>, wo:<id>, or repo:<id>")
	set.StringVar(&request.Reversibility, "reversibility", "", "reversible or one_way")
	set.StringVar(&request.QuestionID, "question", "", "the question it answers")
	set.StringVar(&request.Supersedes, "supersedes", "", "the decision it replaces, with no rework")
	set.Var(&noConflict, "no-conflict", "a decision it overlaps and does not conflict with, repeatable")
	set.Var(&categories, "category", "a category, repeatable")
	set.Var(&citations, "citation", "what it rests on, repeatable")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if request.Statement == "" {
		return c.usageError("--statement is required")
	}
	if _, err := questions.ParseScope(request.Scope); err != nil {
		return c.usageError("--scope: %v", err)
	}
	if request.ID == "" {
		id, err := newID()
		if err != nil {
			return c.failure(err)
		}
		request.ID = id
	}
	request.Status = questions.DecisionDecided
	if *assumed {
		request.Status = questions.DecisionAssumed
	}
	request.Categories, request.Citations, request.NoConflict = categories, citations, noConflict
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.RecordDecision(c.ctx, request)
	return c.finish(err, row, func() string { return decisionLine(api.DecisionView{Decision: row}) })
}

func (c *invocation) decisionOverturn(args []string) int {
	set := c.flags()
	var request api.OverturnDecision
	var citations repeated
	set.StringVar(&request.ID, "id", "", "a client-generated ID for the decision that takes its place")
	set.StringVar(&request.Statement, "statement", "", "what stands now")
	set.StringVar(&request.Rationale, "rationale", "", "why")
	set.Var(&citations, "citation", "what it rests on, repeatable")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one decision ID")
	}
	if request.Statement == "" {
		return c.usageError("--statement is required")
	}
	if request.ID == "" {
		id, err := newID()
		if err != nil {
			return c.failure(err)
		}
		request.ID = id
	}
	request.Citations = citations
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.OverturnDecision(c.ctx, positional[0], request)
	return c.finish(err, row, func() string {
		var out strings.Builder
		out.WriteString(decisionLine(api.DecisionView{Decision: row.Old}))
		out.WriteString(decisionLine(api.DecisionView{Decision: row.New}))
		for _, id := range row.Rework {
			out.WriteString("rework\t" + id + "\n")
		}
		for _, lane := range row.Noticed {
			out.WriteString("noticed\t" + lane + "\n")
		}
		return out.String()
	})
}

func policyLine(p authority.PolicyVersion) string {
	return fmt.Sprintf("%d\t%s\t%s\n", p.Version, p.AuthoredBy, p.Digest)
}

// policyKind reads `--kind`, which names the decision policy unless it says otherwise.
func policyKind(kind string) (authority.PolicyKind, bool) {
	switch authority.PolicyKind(kind) {
	case authority.PolicyDecision, authority.PolicyLane, authority.PolicyDelegation, authority.PolicyAutonomy, authority.PolicyNotify, authority.PolicyRecovery,
		authority.PolicySpend, authority.PolicyInstallationSpend, delivery.PolicyEstimate:
		return authority.PolicyKind(kind), true
	}
	return "", false
}

func (c *invocation) policyList(args []string) int {
	set := c.flags()
	kind := set.String("kind", string(authority.PolicyDecision), "the policy: decision, lane, delegation, autonomy, notify, recovery, spend, or estimate")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	named, ok := policyKind(*kind)
	if !ok {
		return c.usageError("no policy %q", *kind)
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	rows, err := client.PoliciesOf(c.ctx, named)
	return c.finish(err, api.PolicyList{Versions: rows}, func() string {
		var out strings.Builder
		for _, row := range rows {
			out.WriteString(policyLine(row))
		}
		return out.String()
	})
}

func (c *invocation) policyShow(args []string) int {
	set := c.flags()
	kind := set.String("kind", string(authority.PolicyDecision), "the policy: decision, lane, delegation, autonomy, notify, recovery, spend, or estimate")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one version")
	}
	version, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil {
		return c.usageError("version %q is not a number", positional[0])
	}
	named, ok := policyKind(*kind)
	if !ok {
		return c.usageError("no policy %q", *kind)
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.PolicyOf(c.ctx, named, version)
	return c.finish(err, row, func() string { return row.Body })
}

func (c *invocation) policyPublish(args []string) int {
	set := c.flags()
	file := set.String("file", "", "the TOML table to publish")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if *file == "" {
		return c.usageError("--file is required")
	}
	body, err := c.readLocal(*file)
	if err != nil {
		return c.failure(err)
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.PublishPolicy(c.ctx, string(body))
	return c.finish(err, row, func() string {
		if row.Notice != "" {
			return policyLine(row.PolicyVersion) + "notice\t" + row.Notice + "\n"
		}
		return policyLine(row.PolicyVersion)
	})
}

func (c *invocation) categoryList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	names, err := client.Categories(c.ctx)
	return c.finish(err, api.CategoryList{Categories: names}, func() string {
		return strings.Join(names, "\n") + "\n"
	})
}

func (c *invocation) categoryAdd(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one category name")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	err := client.AddCategory(c.ctx, positional[0])
	return c.finish(err, api.AddCategory{Name: positional[0]}, func() string { return positional[0] + "\n" })
}

// search reads candidates. The two-word table of commands has no entry for one word, so
// `ropeshift search` reaches this through its own case.
func (c *invocation) search(args []string) int {
	set := c.flags()
	var kinds repeated
	set.Var(&kinds, "kind", "decision, requirement, contract, decision_document, or question, repeatable")
	limit := set.Int("limit", 0, "the most candidates to show")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) == 0 {
		return c.usageError("takes the words to search for")
	}
	if len(kinds) == 0 {
		kinds = append(questions.LookupKinds(), questions.OpenKinds()...)
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	found, err := client.Candidates(c.ctx, strings.Join(positional, " "), kinds, *limit)
	return c.finish(err, api.CandidateList{Candidates: found}, func() string {
		var out strings.Builder
		for _, candidate := range found {
			fmt.Fprintf(&out, "%s\t%s\t%.3f\n", candidate.Kind, candidate.RecordID, candidate.Score)
		}
		return out.String()
	})
}
