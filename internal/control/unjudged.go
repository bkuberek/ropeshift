package control

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// unjudgedHoldMs is how long the core holds a question it takes to a person: the one write that takes it ends the
// hold, so this only keeps an orchestrator's take from landing inside that write.
const unjudgedHoldMs = 1

// Matching sets what finds, by text, the candidates the core lists on a question no orchestrator took.
func (q *Questions) Matching(matcher ports.Matcher) *Questions {
	q.matcher = matcher
	return q
}

// escalateUnjudged takes to a person every question no live orchestrator holds whose batch window has passed since it
// was asked (FR-QD-3): an orchestrator that never started, waits out a usage limit, has a tripped breaker, or runs on a
// machine that is offline would otherwise leave a lane waiting with no signal. Each question is its own write, and a
// take decides it, so an orchestrator that takes the question first keeps it.
func (q *Questions) escalateUnjudged(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) error {
	if leavesUnjudged() {
		return nil
	}
	var due []questions.Question
	err := q.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		free, err := freeQuestions(tx, "")
		if err != nil {
			return err
		}
		due = slices.DeleteFunc(free, func(row questions.Question) bool { return row.AskedAt+q.batches.WindowMs > now })
		return nil
	})
	if err != nil {
		return err
	}
	var failed []error
	for _, row := range due {
		if err := q.escalateOne(ctx, scope, row, actor); err != nil {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

// escalateOne runs the lookup rungs by text, records that no orchestrator worked the rest, and gathers the question
// into the open batch, which closes now: the question waited its window already. A search that fails lists nothing,
// and the question still reaches a person, so a search that stays down never holds a lane's question back; the failure
// is said once, by the tick that took the question.
func (q *Questions) escalateOne(ctx context.Context, scope ledger.WorkspaceID, row questions.Question, actor ledger.Actor) error {
	lookup, open, searched := q.lexical(ctx, scope, row)
	if searched != nil {
		lookup, open = nil, nil
		searched = fmt.Errorf("question %s reached a person with no candidates: %w", row.ID, searched)
	}
	escalated := false
	err := q.store.Write(ctx, scope, "question.unjudged", func(tx ports.WriteTx) error {
		escalated = false
		won, err := tx.Questions().Take(row.ID, actor.ID, unjudgedHoldMs)
		if err != nil || !won {
			return err
		}
		record, err := tx.Questions().Question(row.ID)
		if err != nil {
			return err
		}
		policy, err := policyInForce(tx)
		if err != nil {
			return err
		}
		record = questions.Unjudged(record, policy, lookup, open)
		if record.Overlaps, err = overlapsOf(tx, record); err != nil {
			return err
		}
		crashpoint.Hit("question.unjudged", "climbed")
		if err := q.gather(tx, &record, actor); err != nil {
			return err
		}
		batch, err := tx.Questions().Batch(record.BatchID)
		if err != nil {
			return err
		}
		if err := hastenTo(tx, batch, tx.Now(), actor); err != nil {
			return err
		}
		escalated = true
		return saveQuestion(tx, record, actor)
	})
	if err != nil || !escalated {
		return err
	}
	return searched
}

// lexical is what the core finds by text for rungs 1 and 2, under the names an orchestrator's ladder read gives them.
func (q *Questions) lexical(ctx context.Context, scope ledger.WorkspaceID, row questions.Question) (lookup, open []string, err error) {
	if q.matcher == nil {
		return nil, nil, nil
	}
	found, err := q.matcher.Candidates(ctx, scope, ports.MatchRequest{Text: row.Text, Kinds: questions.LookupKinds(), Limit: candidateLimit})
	if err != nil {
		return nil, nil, err
	}
	asked, err := q.matcher.Candidates(ctx, scope, ports.MatchRequest{Text: row.Text, Kinds: questions.OpenKinds(), Limit: candidateLimit + 1})
	if err != nil {
		return nil, nil, err
	}
	asked = slices.DeleteFunc(asked, func(candidate ports.Candidate) bool { return candidate.RecordID == row.ID })
	err = q.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		if lookup, err = candidateRefs(tx, found); err != nil {
			return err
		}
		open, err = candidateRefs(tx, asked)
		return err
	})
	return lookup, open, err
}

func candidateRefs(tx ports.ReadTx, found []ports.Candidate) ([]string, error) {
	read, err := readCandidates(tx, found)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(read))
	for _, candidate := range read {
		refs = append(refs, candidate.Ref)
	}
	return refs, nil
}
