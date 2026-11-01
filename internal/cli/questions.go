package cli

import (
	"flag"
	"fmt"
	"strings"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/questions"
)

// filterFlags adds the flags a list of questions or decisions shares.
func filterFlags(set *flag.FlagSet, filter *api.RecordFilter) {
	set.StringVar(&filter.Category, "category", "", "only this category")
	set.StringVar(&filter.Status, "status", "", "only this status")
	set.StringVar(&filter.Scope, "scope", "", "only this scope")
	set.StringVar(&filter.Text, "text", "", "only records that match these words, best first")
}

func questionLine(q questions.Question) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s\n", q.ID, q.Status, strings.Join(q.Categories, ","), q.Text)
}

func decisionLine(d questions.Decision) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s\n", d.ID, d.Status, d.Scope, d.Statement)
}

func (c *invocation) questionList(args []string) int {
	set := c.flags()
	var filter api.RecordFilter
	filterFlags(set, &filter)
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client := c.client()
	defer client.Close()
	rows, err := client.Questions(c.ctx, filter)
	if rows == nil {
		rows = []questions.Question{}
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
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one question ID")
	}
	client := c.client()
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
	set.StringVar(&request.SubjectKind, "subject-kind", "", "contract or work_order")
	set.BoolVar(&request.Blocking, "blocking", false, "work on the subject waits for the answer")
	positional, ok := parse(set, args)
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
	client := c.client()
	defer client.Close()
	row, err := client.RecordQuestion(c.ctx, request)
	return c.finish(err, row, func() string { return questionLine(row) })
}

func (c *invocation) questionDismiss(args []string) int {
	set := c.flags()
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one question ID")
	}
	client := c.client()
	defer client.Close()
	err := client.DismissQuestion(c.ctx, positional[0])
	return c.finish(err, struct {
		Dismissed string `json:"dismissed"`
	}{positional[0]}, func() string { return positional[0] + "\tdismissed\n" })
}

func (c *invocation) decisionList(args []string) int {
	set := c.flags()
	var filter api.RecordFilter
	filterFlags(set, &filter)
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client := c.client()
	defer client.Close()
	rows, err := client.Decisions(c.ctx, filter)
	if rows == nil {
		rows = []questions.Decision{}
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
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one decision ID")
	}
	client := c.client()
	defer client.Close()
	row, err := client.Decision(c.ctx, positional[0])
	return c.finish(err, row, func() string { return decisionLine(row) })
}

func (c *invocation) decisionRecord(args []string) int {
	set := c.flags()
	var request api.RecordDecision
	var categories, citations repeated
	assumed := set.Bool("assumed", false, "record a reversible assumption, not a decision")
	set.StringVar(&request.ID, "id", "", "a client-generated ID")
	set.StringVar(&request.Statement, "statement", "", "what was decided")
	set.StringVar(&request.Rationale, "rationale", "", "why")
	set.StringVar(&request.Scope, "scope", "", "what it applies to")
	set.StringVar(&request.Reversibility, "reversibility", "", "reversible or one_way")
	set.StringVar(&request.QuestionID, "question", "", "the question it answers")
	set.Var(&categories, "category", "a category, repeatable")
	set.Var(&citations, "citation", "what it rests on, repeatable")
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
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
	request.Status = questions.DecisionDecided
	if *assumed {
		request.Status = questions.DecisionAssumed
	}
	request.Categories, request.Citations = categories, citations
	client := c.client()
	defer client.Close()
	row, err := client.RecordDecision(c.ctx, request)
	return c.finish(err, row, func() string { return decisionLine(row) })
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
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one category name")
	}
	client := c.client()
	defer client.Close()
	err := client.AddCategory(c.ctx, positional[0])
	return c.finish(err, api.AddCategory{Name: positional[0]}, func() string { return positional[0] + "\n" })
}

// search reads candidates. The two-word table of commands has no entry for one word, so
// `ropeshift search` reaches this through its own case.
func (c *invocation) search(args []string) int {
	set := c.flags()
	var kinds repeated
	set.Var(&kinds, "kind", "decision, requirement, contract, or question, repeatable")
	limit := set.Int("limit", 0, "the most candidates to show")
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) == 0 {
		return c.usageError("takes the words to search for")
	}
	if len(kinds) == 0 {
		kinds = append(questions.LookupKinds(), questions.OpenKinds()...)
	}
	client := c.client()
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
