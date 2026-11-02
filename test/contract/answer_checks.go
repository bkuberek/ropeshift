package contract

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// AnswerChecks are what an answer owes the lanes that asked (FR-QD-10). Each runs through the ledger
// alone: a worker's half is in the worker's tests.
var AnswerChecks = []Check{
	{"AN1", "an answer reaches every attached lane, in the write that closes the question", checkAnswerReachesLanes},
	{"AN2", "a parked lane's answer is a fact of its next generation, and its wake fires", checkAnswerParkedLane},
	{"AN3", "every way of closing a question tells its lanes", checkAnswerEveryPath},
	{"AN4", "a retried answer sends nothing twice", checkAnswerRetry},
	{"AN5", "a lane with no record stands aside, and the answer stands", checkAnswerNoRow},
	{"AN6", "an answer too long for a message still closes its question", checkAnswerClipped},
	{"AN7", "a dismissed question tells nobody", checkAnswerDismissed},
	{"AN8", "an overturned decision tells each lane, and a parked lane as a fact", checkNoticeOverturned},
	{"AN9", "a replaced decision tells each lane", checkNoticeReplaced},
	{"AN10", "a lane that ended learns nothing it can use, and its sender can read that", checkAnswerEndedLane},
	{"AN11", "a notice too long for a message still ends its decision", checkNoticeClipped},
}

func (r *Rig) messagesTo(s ports.Store, lane string) ([]messages.Message, error) {
	var rows []messages.Message
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.Messages().ToLane(lane)
		return err
	})
	return rows, err
}

// answerTo reads the one answer a lane holds.
func (r *Rig) answerTo(s ports.Store, lane, question string) (messages.Message, error) {
	rows, err := r.messagesTo(s, lane)
	if err != nil {
		return messages.Message{}, err
	}
	if len(rows) != 1 {
		return messages.Message{}, Violatedf("lane %s holds %+v, want one answer", lane, rows)
	}
	if rows[0].Kind != messages.KindAnswer || rows[0].ReplyTo != question {
		return messages.Message{}, Violatedf("lane %s holds %+v", lane, rows[0])
	}
	return rows[0], nil
}

const askedText = "How often should a failed payment retry?"

// twoAsking has two running lanes on one question, and returns its ID.
func twoAsking(r *Rig) (*statusRig, *statusRig, string, error) {
	first, err := newStatusRig(r)
	if err != nil {
		return nil, nil, "", err
	}
	second, err := newStatusRig(r)
	if err != nil {
		return nil, nil, "", err
	}
	asked, err := r.ask(first.store, askedText, first.lane, nil)
	if err != nil {
		return nil, nil, "", err
	}
	attached, err := r.ask(first.store, askedText, second.lane, nil)
	if err != nil {
		return nil, nil, "", err
	}
	if attached.QuestionID != asked.QuestionID {
		return nil, nil, "", Violatedf("the second ask opened %s", attached.QuestionID)
	}
	return first, second, asked.QuestionID, nil
}

func checkAnswerReachesLanes(r *Rig) error {
	first, second, question, err := twoAsking(r)
	if err != nil {
		return err
	}
	if _, err := r.answer(first.store, question, "Twice."); err != nil {
		return err
	}
	for _, lane := range []string{first.lane, second.lane} {
		m, err := r.answerTo(first.store, lane, question)
		if err != nil {
			return err
		}
		if m.State != messages.Queued {
			return Violatedf("a running lane's answer reads %+v", m)
		}
		if !strings.Contains(m.Body, askedText) || !strings.Contains(m.Body, "Twice.") || !strings.Contains(m.Body, "It follows.") {
			return Violatedf("an answer reads %q", m.Body)
		}
	}
	offered, err := first.offered()
	if err != nil {
		return err
	}
	if len(offered) != 1 || offered[0].Kind != string(messages.KindAnswer) {
		return Violatedf("desired state offers %+v", offered)
	}
	return r.CheckMessages(first.store)
}

func checkAnswerParkedLane(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	asked, err := r.ask(s.store, askedText, s.lane, nil)
	if err != nil {
		return err
	}
	wake := r.ID("wake")
	if err := accepted(s.parkingUpload(wake, lanes.QuestionWake([]string{asked.QuestionID}))); err != nil {
		return err
	}
	if err := accepted(s.parkedUpload(wake, true)); err != nil {
		return err
	}
	if _, err := r.answer(s.store, asked.QuestionID, "Twice."); err != nil {
		return err
	}
	m, err := r.answerTo(s.store, s.lane, asked.QuestionID)
	if err != nil {
		return err
	}
	if m.State != messages.Expired || m.Reason != messages.ReasonLedgerFact {
		return Violatedf("a parked lane's answer reads %+v", m)
	}
	stored, err := s.wake(wake)
	if err != nil {
		return err
	}
	if stored.Open() {
		return Violatedf("the wake waits still: %+v", stored)
	}
	grant, err := s.claim()
	if err != nil {
		return err
	}
	s.grant = grant
	facts, err := s.leaseFacts()
	if err != nil {
		return err
	}
	if len(facts) != 1 || !strings.Contains(facts[0].Body, "Twice.") {
		return Violatedf("a next generation owes %+v", facts)
	}
	return r.CheckMessages(s.store)
}

// checkAnswerEveryPath closes a question each way the ledger can.
func checkAnswerEveryPath(r *Rig) error {
	for name, closed := range map[string]func(s *statusRig, text string, attach func(*questions.Ask)) (string, error){
		"a member's answer": func(s *statusRig, text string, attach func(*questions.Ask)) (string, error) {
			asked, err := r.ask(s.store, text, s.lane, attach)
			if err != nil {
				return "", err
			}
			_, err = r.answer(s.store, asked.QuestionID, "Twice.")
			return asked.QuestionID, err
		},
		"an orchestrator's ruling": func(s *statusRig, text string, attach func(*questions.Ask)) (string, error) {
			question, holder, err := r.OnRung(s.store, text, questions.RungDecide, attach)
			if err != nil {
				return "", err
			}
			_, err = r.NewOps(s.store).Rule(r.Ctx, r.Scope, question, ruling(r.ID("d"), questions.DecisionDecided), holder)
			return question, err
		},
		"a decision the ledger holds": func(s *statusRig, text string, attach func(*questions.Ask)) (string, error) {
			asked, err := r.ask(s.store, text, s.lane, attach)
			if err != nil {
				return "", err
			}
			decided, err := r.NewOps(s.store).RecordDecision(r.Ctx, r.Scope, questions.Decision{
				ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Twice.", Rationale: "It follows.",
				Citations: []string{"PAY-12"}, Scope: "scope/" + r.ID("s"), Reversibility: questions.Reversible,
			}, r.Actor)
			if err != nil {
				return "", err
			}
			return asked.QuestionID, r.resolvedBy(s.store, asked.QuestionID, decided.ID)
		},
	} {
		s, err := newStatusRig(r)
		if err != nil {
			return err
		}
		question, err := closed(s, askedText+" "+r.ID("t"), func(a *questions.Ask) { a.Lane = s.lane })
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := r.answerTo(s.store, s.lane, question); err != nil {
			return Violatedf("%s: %v", name, err)
		}
		if err := r.CheckMessages(s.store); err != nil {
			return err
		}
	}
	return nil
}

func checkAnswerRetry(r *Rig) error {
	first, _, question, err := twoAsking(r)
	if err != nil {
		return err
	}
	answer := questions.Answer{DecisionID: r.ID("d"), Statement: "Twice.", Rationale: "It follows."}
	ops := r.NewOps(first.store)
	for range 2 {
		if _, err := ops.Answer(r.Ctx, r.Scope, question, answer, r.Actor); err != nil {
			return err
		}
	}
	if _, err := r.answerTo(first.store, first.lane, question); err != nil {
		return err
	}
	return r.CheckMessages(first.store)
}

// checkAnswerNoRow keeps an ask a lane made before any record of that lane reached the ledger. Its
// setup needs the skip: the answer fails without it.
func checkAnswerNoRow(r *Rig) error {
	s := r.Store()
	asked, err := r.ask(s, askedText, "no-such-lane", nil)
	if err != nil {
		return err
	}
	if _, err := r.answer(s, asked.QuestionID, "Twice."); err != nil {
		return err
	}
	read, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if read.Status != questions.StatusAnswered {
		return Violatedf("the question reads %s", read.Status)
	}
	rows, err := r.messagesTo(s, "no-such-lane")
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		return Violatedf("a message to no lane: %+v", rows)
	}
	return nil
}

func checkAnswerClipped(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	asked, err := r.ask(s.store, askedText, s.lane, nil)
	if err != nil {
		return err
	}
	statement := "Twice. " + strings.Repeat("é", messages.MaxBody)
	decided, err := r.answer(s.store, asked.QuestionID, statement)
	if err != nil {
		return err
	}
	m, err := r.answerTo(s.store, s.lane, asked.QuestionID)
	if err != nil {
		return err
	}
	if len(m.Body) > messages.MaxBody || !strings.Contains(m.Body, "Twice.") || !strings.Contains(m.Body, decided.ID) {
		return Violatedf("a clipped answer holds %d bytes", len(m.Body))
	}
	if !strings.HasSuffix(strings.TrimSpace(m.Body), messages.Clipped) {
		return Violatedf("a clipped answer does not say so: %q", m.Body[len(m.Body)-40:])
	}
	return r.CheckMessages(s.store)
}

func checkAnswerDismissed(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	asked, err := r.ask(s.store, askedText, s.lane, nil)
	if err != nil {
		return err
	}
	if err := r.NewOps(s.store).Dismiss(r.Ctx, r.Scope, asked.QuestionID, r.Actor); err != nil {
		return err
	}
	rows, err := r.messagesTo(s.store, s.lane)
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		return Violatedf("a question nobody answered told a lane: %+v", rows)
	}
	return nil
}

// noticesTo reads what a lane was told of decisions.
func (r *Rig) noticesTo(s ports.Store, lane string) ([]messages.Message, error) {
	rows, err := r.messagesTo(s, lane)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(rows, func(m messages.Message) bool { return m.Kind != messages.KindDecisionNotice }), nil
}

func checkNoticeOverturned(r *Rig) error {
	running, parked, question, err := twoAsking(r)
	if err != nil {
		return err
	}
	old, err := r.answer(running.store, question, "Twice.")
	if err != nil {
		return err
	}
	wake := parked.lane + "/parked"
	if err := accepted(parked.parkingUpload(wake, `{"human_resume":true}`)); err != nil {
		return err
	}
	if err := accepted(parked.parkedUpload(wake, true)); err != nil {
		return err
	}
	overturned, err := r.overturn(running.store, old.ID)
	if err != nil {
		return err
	}
	if !slices.Contains(overturned.Noticed, running.lane) || !slices.Contains(overturned.Noticed, parked.lane) {
		return Violatedf("noticed %v", overturned.Noticed)
	}
	for lane, want := range map[string]messages.State{running.lane: messages.Queued, parked.lane: messages.Expired} {
		rows, err := r.noticesTo(running.store, lane)
		if err != nil {
			return err
		}
		if len(rows) != 1 || rows[0].State != want || !strings.Contains(rows[0].Body, old.ID) || !strings.Contains(rows[0].Body, "Retries log at warn.") {
			return Violatedf("lane %s holds %+v", lane, rows)
		}
		if want == messages.Expired && rows[0].Reason != messages.ReasonLedgerFact {
			return Violatedf("a parked lane's notice reads %+v", rows[0])
		}
	}
	return r.CheckMessages(running.store)
}

func checkNoticeReplaced(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	asked, err := r.ask(s.store, askedText, s.lane, nil)
	if err != nil {
		return err
	}
	old, err := r.answer(s.store, asked.QuestionID, "Twice.")
	if err != nil {
		return err
	}
	draft := questions.Decision{
		ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Three times.", Rationale: "It follows.",
		Citations: []string{"PAY-12"}, Scope: "scope/" + r.ID("s"), Reversibility: questions.Reversible, Supersedes: old.ID,
	}
	if _, err := r.NewOps(s.store).RecordDecision(r.Ctx, r.Scope, draft, r.Actor); err != nil {
		return err
	}
	rows, err := r.noticesTo(s.store, s.lane)
	if err != nil {
		return err
	}
	if len(rows) != 1 || !strings.Contains(rows[0].Body, "Three times.") {
		return Violatedf("a replaced decision told %+v", rows)
	}
	return r.CheckMessages(s.store)
}

func checkNoticeClipped(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	asked, err := r.ask(s.store, askedText, s.lane, nil)
	if err != nil {
		return err
	}
	old, err := r.answer(s.store, asked.QuestionID, "Twice.")
	if err != nil {
		return err
	}
	overturn := questions.Overturn{DecisionID: r.ID("d"), Statement: strings.Repeat("é", messages.MaxBody), Rationale: "A person read the logs."}
	if _, err := r.NewOps(s.store).Overturn(r.Ctx, r.Scope, old.ID, overturn, r.Actor); err != nil {
		return err
	}
	rows, err := r.noticesTo(s.store, s.lane)
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0].Body) > messages.MaxBody {
		return Violatedf("a notice of %d rows", len(rows))
	}
	return r.CheckMessages(s.store)
}

// checkAnswerEndedLane answers a lane that has no next generation, which reads as what it is.
func checkAnswerEndedLane(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	asked, err := r.ask(s.store, askedText, s.lane, nil)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}})
	if err != nil {
		return err
	}
	if err := accepted(s.upload(ledger.UploadLaneEnded, raw)); err != nil {
		return err
	}
	if _, err := r.answer(s.store, asked.QuestionID, "Twice."); err != nil {
		return err
	}
	m, err := r.answerTo(s.store, s.lane, asked.QuestionID)
	if err != nil {
		return err
	}
	if m.State != messages.Expired || m.Reason != messages.ReasonGenerationEnded {
		return Violatedf("an ended lane's answer reads %+v", m)
	}
	return r.CheckMessages(s.store)
}
