package cli

import (
	"flag"
	"fmt"
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
		authority.PolicySpend, delivery.PolicyEstimate:
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
