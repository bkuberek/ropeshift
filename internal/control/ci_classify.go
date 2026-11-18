package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The classification of a head's finished checks (ADR-0037, FR-SPEND-22). It runs in the transaction that keeps a read
// of the pull request, before any session starts, and it starts none: a failure of the code goes back to rework through
// the integration and acceptance rules with the failing job's log, and every other class is a rerun the worker makes, a
// finding a person answers, or nothing at all.

// The classes that allow a rerun.
const (
	ciClassInfrastructure = "infrastructure"
	ciClassMember         = "member"
)

// ciActor is the control plane acting on CI by rule.
var ciActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "ci"}

// rerunID names one rerun of a head. The automatic ones and a member's are numbered apart, so neither's number moves
// the other's, and a rerun asked for twice is made once.
func rerunID(workOrder, head string, auto bool, n int64) string {
	who := ciClassMember
	if auto {
		who = "auto"
	}
	return fmt.Sprintf("rerun:%s:%s:%s:%d", who, workOrder, head, n)
}

// rerunIsAuto says a rerun request is one of the control plane's.
func rerunIsAuto(request ledger.ForgeRequest) bool {
	return strings.HasPrefix(request.ID, "rerun:auto:")
}

// logsID names the read of the logs of one head's failing checks, one for each time they failed: it ends with a
// fingerprint of the runs and jobs the checks name and when each finished, so a rerun that fails again is read again and
// never gives the brief the log of the attempt before it.
func logsID(workOrder, head string, failing []ledger.PullRequestCheck) string {
	lines := make([]string, 0, len(failing))
	for _, check := range failing {
		lines = append(lines, check.Name+"\x00"+check.DetailsURI+"\x00"+check.CompletedAt)
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "logs:" + workOrder + ":" + head + ":" + hex.EncodeToString(sum[:6])
}

// classifyCI classifies a work order's head once every required check of it finished. A head with a test failure is
// left to the rules that send work back. Otherwise a check that waits for a person, and a failure the base branch's
// tip shares, are raised as findings, and an infrastructure failure is rerun up to `ci_auto_rerun` times and then
// raised. A head whose checks all pass has nothing raised. None of it starts a lane.
func classifyCI(tx ports.WriteTx, workOrder string) error {
	pr, err := tx.Forge().PullRequest(workOrder)
	if err != nil {
		return err
	}
	if pr.State != ledger.PullRequestOpen {
		return nil
	}
	order, err := tx.WorkOrders().Get(workOrder)
	if err != nil {
		return err
	}
	if workorders.Terminal(order.State) {
		return nil
	}
	byClass := map[ledger.CICheckClass][]ledger.PullRequestCheck{}
	for _, check := range pr.Checks {
		if !check.Required {
			continue
		}
		switch class := check.Class(); class {
		case ledger.CIClassPending:
			return nil
		case ledger.CIClassPass:
		default:
			byClass[class] = append(byClass[class], check)
		}
	}
	if len(byClass) == 0 {
		_, err := tx.CI().ClearCIFindings(workOrder, pr.HeadCommit)
		return err
	}
	if len(byClass[ledger.CIClassTestFailure]) != 0 {
		return nil
	}
	record, err := tx.Repositories().Repository(pr.Repository)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, raised := range []struct {
		class ledger.CICheckClass
		kind  string
	}{{ledger.CIClassActionRequired, ledger.CIItemActionRequired}, {ledger.CIClassBaseRed, ledger.CIItemBaseRed}} {
		if checks := byClass[raised.class]; len(checks) != 0 {
			if _, err := keepCIFinding(tx, findingOf(pr, raised.kind, checks[0])); err != nil {
				return err
			}
		}
	}
	if checks := byClass[ledger.CIClassInfrastructure]; len(checks) != 0 {
		return handleInfrastructure(tx, pr, record, checks)
	}
	return nil
}

func findingOf(pr ledger.WorkPullRequest, kind string, check ledger.PullRequestCheck) ledger.CIFinding {
	return ledger.CIFinding{
		WorkOrder: pr.WorkOrder, Head: pr.HeadCommit, Kind: kind, Check: check.Name, Conclusion: check.Conclusion,
		URL: check.DetailsURI, BaseCommit: check.RedOnBase,
	}
}

// handleInfrastructure reruns what a runner or the forge broke, or raises it. A failure that finished before the
// last rerun was asked for is the one that rerun is for, and waits for its answer. The rerun is automatic only while
// the head holds fewer automatic reruns than `ci_auto_rerun`, whatever its members rerun themselves, and only when
// every failed check has a run the forge's rerun reaches: for any other it is a person's.
func handleInfrastructure(tx ports.WriteTx, pr ledger.WorkPullRequest, record ledger.Repository, checks []ledger.PullRequestCheck) error {
	waits, err := tx.CI().CIWaits()
	if err != nil {
		return err
	}
	var waiting ledger.CIWaiting
	for _, held := range waits {
		if held.WorkOrder == pr.WorkOrder && held.Head == pr.HeadCommit {
			waiting = held.CIWaiting
		}
	}
	var fresh []ledger.PullRequestCheck
	for _, check := range checks {
		if check.CompletedAfter(waiting.RerunAt) {
			fresh = append(fresh, check)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	binding, bound := ledger.ParseForgeBinding(record.ForgeBinding)
	refs := make([]ledger.CICheckRef, 0, len(fresh))
	reachable := bound == nil
	for _, check := range fresh {
		key := ledger.ActionsKey(check.DetailsURI, binding)
		refs = append(refs, ledger.CICheckRef{Name: check.Name, URL: check.DetailsURI, Key: key})
		if key == "" {
			reachable = false
		}
	}
	if !reachable || waiting.AutoReruns >= record.CI.Effective().AutoRerun {
		_, err := keepCIFinding(tx, findingOf(pr, ledger.CIItemInfrastructure, fresh[0]))
		return err
	}
	_, err = requestRerun(tx, rerunAsked{
		pr: pr, repository: record, kind: ledger.CIItemInfrastructure, class: ciClassInfrastructure, auto: true, refs: refs, check: fresh[0],
	}, ciActor)
	return err
}

// rerunAsked is a rerun of a head's checks to be requested of a worker.
type rerunAsked struct {
	pr         ledger.WorkPullRequest
	repository ledger.Repository
	// kind is the item that offers it or the finding that allows it, and class what allowed it.
	kind, class string
	auto        bool
	// refs name the checks whose runs a rerun is for. A member's rerun names none, and reruns every run of the head
	// that finished without passing.
	refs  []ledger.CICheckRef
	check ledger.PullRequestCheck
}

// requestRerun counts a rerun of a head, as automatic or as a member's, and asks the worker that opened the pull
// request to make it. Each rerun is an event with its class and its number among its kind, and the forge call is the
// worker's, through the forge's port.
func requestRerun(tx ports.WriteTx, asked rerunAsked, actor ledger.Actor) (ledger.CIWaiting, error) {
	pr := asked.pr
	waiting, err := tx.CI().CountCIRerun(pr.WorkOrder, pr.HeadCommit, tx.Now(), asked.auto)
	if err != nil {
		return ledger.CIWaiting{}, err
	}
	attempt := waiting.Reruns - waiting.AutoReruns
	if asked.auto {
		attempt = waiting.AutoReruns
	}
	request := ledger.ForgeRequest{
		ID: rerunID(pr.WorkOrder, pr.HeadCommit, asked.auto, attempt), Kind: ledger.ForgeRerun, WorkOrder: pr.WorkOrder, Repository: pr.Repository,
		Worker: pr.Worker, Binding: asked.repository.ForgeBinding, Commit: pr.HeadCommit, Number: pr.Number, RequestedAt: tx.Now(),
	}
	if len(asked.refs) != 0 {
		raw, err := json.Marshal(asked.refs)
		if err != nil {
			return ledger.CIWaiting{}, err
		}
		request.Body = string(raw)
	}
	first, err := tx.Forge().InsertRequest(request)
	if err != nil {
		return ledger.CIWaiting{}, err
	}
	if first {
		if err := appendForge(tx, ledger.EventForgeRequested, pr.WorkOrder, request, actor); err != nil {
			return ledger.CIWaiting{}, err
		}
	}
	return waiting, appendCI(tx, ledger.EventCIRerunRequested, pr.WorkOrder, ciEvent{
		Head: pr.HeadCommit, Kind: asked.kind, Reruns: waiting.Reruns, Auto: asked.auto, Attempt: attempt, Class: asked.class, Request: request.ID,
		Check: asked.check.Name, Conclusion: asked.check.Conclusion,
	}, actor)
}

// rerunReported keeps what a worker did with a rerun, as an event with the runs the forge started again or why it
// refused. A rerun the control plane made that started nothing leaves the head's failure where it was, with nothing
// on its way, so it is raised for a person.
func rerunReported(tx ports.WriteTx, request ledger.ForgeRequest, report ledger.ForgeReport, id string, at int64, actor ledger.Actor) error {
	var result ledger.CIRerun
	if report.Rerun != nil {
		result = *report.Rerun
	}
	refused := clipCI(ledger.SanitizeCIText(result.Refused), 500)
	pending, dropped := continuable(request.Body, runsOf(result.Pending))
	detail := "reran runs " + strings.Join(runsOf(result.Runs), ", ")
	if len(result.Runs) == 0 {
		detail = "reran no run"
	}
	if refused != "" {
		detail += "; the forge refused: " + refused
	}
	if len(dropped) != 0 {
		detail += "; not continued, because the rerun never named them: " + strings.Join(dropped, ", ")
	}
	if _, err := tx.Forge().FinishRequestWith(request.ID, request.Attempt, tx.Now(), detail, request.Body); err != nil {
		return err
	}
	class := ciClassMember
	if rerunIsAuto(request) {
		class = ciClassInfrastructure
	}
	if err := appendForgeAs(tx, id, at, ledger.EventCIRerunDone, request.WorkOrder, ciEvent{
		Head: request.Commit, Class: class, Auto: rerunIsAuto(request), Request: request.ID, Runs: runsOf(result.Runs), Refused: refused, Pending: pending,
		Dropped: dropped,
	}, actor); err != nil {
		return err
	}
	if len(pending) != 0 {
		return rerunRest(tx, request, pending)
	}
	if !rerunIsAuto(request) || len(result.Runs) != 0 {
		return nil
	}
	pr, err := tx.Forge().PullRequest(request.WorkOrder)
	if err != nil || pr.HeadCommit != request.Commit {
		return err
	}
	for _, check := range pr.Checks {
		if check.Required && check.Class() == ledger.CIClassInfrastructure {
			_, err := keepCIFinding(tx, findingOf(pr, ledger.CIItemInfrastructure, check))
			return err
		}
	}
	return nil
}

// runsOf keeps what a worker reported of runs only where it is a run's number, once, and at most a few of them, because
// a worker's word is not trusted to name what the control plane then asks a forge to touch.
func runsOf(reported []string) []string {
	var runs []string
	for _, run := range reported {
		if run != "" && len(run) <= 20 && strings.Trim(run, "0123456789") == "" && !slices.Contains(runs, run) && len(runs) < 50 {
			runs = append(runs, run)
		}
	}
	return runs
}

// continuable holds the runs a worker says a rerun did not reach to the runs the rerun was for, and says which it
// dropped. A rerun that named runs is for those and no others, so a run the worker names that the request never did is
// not one the control plane asks a forge to touch, whatever the worker says of it. A rerun that named none started again
// every run of the head that finished without passing, so the runs it did not reach are the forge's to say and are all
// continued. A request whose body cannot be read names nothing that can be trusted, and continues nothing.
func continuable(body string, reported []string) (kept, dropped []string) {
	if body == "" {
		return reported, nil
	}
	var refs []ledger.CICheckRef
	var named []string
	if json.Unmarshal([]byte(body), &refs) == nil {
		for _, ref := range refs {
			run, _, _ := strings.Cut(ref.Key, "/")
			named = append(named, run)
		}
	}
	named = runsOf(named)
	for _, run := range reported {
		if slices.Contains(named, run) {
			kept = append(kept, run)
		} else {
			dropped = append(dropped, run)
		}
	}
	return kept, dropped
}

// rerunRest finishes a rerun the forge failed in the middle of. The runs it did not reach are asked for as a
// continuation of the same rerun: a request of the same kind that names the one it continues, an event that says so,
// and no count, because the rerun was counted and passed the cap when it was asked for, so a head is never over
// `ci_auto_rerun` for finishing one. The runs it started again are on record as done and are not asked for again.
func rerunRest(tx ports.WriteTx, request ledger.ForgeRequest, pending []string) error {
	pr, err := tx.Forge().PullRequest(request.WorkOrder)
	if err != nil || pr.HeadCommit != request.Commit || pr.State != ledger.PullRequestOpen {
		return err
	}
	record, err := tx.Repositories().Repository(request.Repository)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	binding, err := ledger.ParseForgeBinding(record.ForgeBinding)
	if err != nil {
		return nil
	}
	refs := make([]ledger.CICheckRef, 0, len(pending))
	for _, run := range pending {
		address := "https://" + binding.Host + "/" + binding.Owner + "/" + binding.Repo + "/actions/runs/" + run
		refs = append(refs, ledger.CICheckRef{Name: "run " + run, URL: address, Key: run + "/"})
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		return err
	}
	next := ledger.ForgeRequest{
		ID: request.ID + ":rest", Kind: ledger.ForgeRerun, WorkOrder: pr.WorkOrder, Repository: pr.Repository, Worker: pr.Worker,
		Binding: record.ForgeBinding, Commit: pr.HeadCommit, Number: pr.Number, Body: string(raw), RequestedAt: tx.Now(),
	}
	first, err := tx.Forge().InsertRequest(next)
	if err != nil || !first {
		return err
	}
	if err := appendForge(tx, ledger.EventForgeRequested, pr.WorkOrder, next, ciActor); err != nil {
		return err
	}
	class := ciClassMember
	if rerunIsAuto(request) {
		class = ciClassInfrastructure
	}
	return appendCI(tx, ledger.EventCIRerunContinued, pr.WorkOrder, ciEvent{
		Head: pr.HeadCommit, Class: class, Auto: rerunIsAuto(request), Request: next.ID, Continues: request.ID, Pending: pending,
	}, ciActor)
}

func clipCI(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) > limit {
		text = text[:limit]
	}
	return text
}

// ciRework says why work goes back for the failing checks of its pull request, and whether it can go back now. A
// check that failed in the repository's own GitHub Actions has a log, which the brief carries as data, so the worker is
// asked to read it first and the work waits for it: the read is a forge request that fails only while the forge cannot
// be reached, and a log that is not there is an answer. A check of any other CI has none to read, and the brief says so.
func ciRework(tx ports.WriteTx, pr *ledger.WorkPullRequest, lead string, failed []string) (string, bool, error) {
	lead = ledger.CILead(lead)
	if pr == nil {
		return lead, true, nil
	}
	var failing []ledger.PullRequestCheck
	for _, check := range pr.Checks {
		if check.Required && check.Class() == ledger.CIClassTestFailure && slices.Contains(failed, check.Name) {
			failing = append(failing, check)
		}
	}
	if len(failing) == 0 {
		return lead, true, nil
	}
	record, err := tx.Repositories().Repository(pr.Repository)
	if errors.Is(err, ports.ErrNotFound) {
		return lead, true, nil
	}
	if err != nil {
		return "", false, err
	}
	binding, bound := ledger.ParseForgeBinding(record.ForgeBinding)
	var refs []ledger.CICheckRef
	if bound == nil {
		for _, check := range failing {
			if key := ledger.ActionsKey(check.DetailsURI, binding); key != "" {
				refs = append(refs, ledger.CICheckRef{Name: check.Name, URL: check.DetailsURI, Key: key})
			}
		}
	}
	read := map[string]ledger.CICheckLog{}
	if len(refs) != 0 {
		request, err := tx.Forge().Request(logsID(pr.WorkOrder, pr.HeadCommit, failing))
		switch {
		case errors.Is(err, ports.ErrNotFound):
			return "", false, requestLogs(tx, *pr, record, refs, failing)
		case err != nil:
			return "", false, err
		case request.State != ledger.ForgeDone:
			return "", false, nil
		}
		var logs []ledger.CICheckLog
		if json.Unmarshal([]byte(request.Body), &logs) == nil {
			for _, log := range logs {
				read[log.Key] = log
			}
		}
	}
	checks := make([]ledger.CIFailedCheck, 0, len(failing))
	for _, check := range failing {
		// A log is a check's by the run and job its address names, which two checks of one name do not share.
		key := ""
		if bound == nil {
			key = ledger.ActionsKey(check.DetailsURI, binding)
		}
		log, ok := read[key]
		ok = ok && key != ""
		checks = append(checks, ledger.CIFailedCheck{Name: check.Name, Link: ledger.ActionsRunURL(check.DetailsURI, binding), Log: log, Read: ok})
	}
	return ledger.CIReworkBrief(lead, int(record.CI.Effective().LogExcerptLines), checks), true, nil
}

// requestLogs asks the worker that opened the pull request to read the logs of the failing checks of its head.
func requestLogs(tx ports.WriteTx, pr ledger.WorkPullRequest, record ledger.Repository, refs []ledger.CICheckRef, failing []ledger.PullRequestCheck) error {
	raw, err := json.Marshal(refs)
	if err != nil {
		return err
	}
	request := ledger.ForgeRequest{
		ID: logsID(pr.WorkOrder, pr.HeadCommit, failing), Kind: ledger.ForgeLogs, WorkOrder: pr.WorkOrder, Repository: pr.Repository, Worker: pr.Worker,
		Binding: record.ForgeBinding, Commit: pr.HeadCommit, Number: pr.Number, Body: string(raw), RequestedAt: tx.Now(),
	}
	first, err := tx.Forge().InsertRequest(request)
	if err != nil || !first {
		return err
	}
	return appendForge(tx, ledger.EventForgeRequested, pr.WorkOrder, request, ciActor)
}

// logsReported keeps the logs a worker read, bounded and scrubbed again by the repository's own keys, because what a
// worker sends is never trusted for coming from the worker. A log is kept only for a check the request named, under
// the address the request gave it, and a worker's own words about a log are dropped. Then the integration rule runs at
// once, which was waiting for it, and the acceptance tick finds it on its next pass.
func logsReported(tx ports.WriteTx, request ledger.ForgeRequest, report ledger.ForgeReport, id string, at int64, actor ledger.Actor) error {
	var refs []ledger.CICheckRef
	if err := json.Unmarshal([]byte(request.Body), &refs); err != nil {
		return fmt.Errorf("%w: the checks of logs request %s: %v", workorders.ErrInvalid, request.ID, err)
	}
	settings := ledger.DefaultCISettings()
	if record, err := tx.Repositories().Repository(request.Repository); err == nil {
		settings = record.CI.Effective()
	} else if !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	// A log is a check's by the run and job its address names, and what a worker reports is matched to the request's own
	// checks and to nothing else.
	reported := map[string]ledger.CICheckLog{}
	for _, log := range report.Logs {
		reported[log.Key] = log
	}
	kept := make([]ledger.CICheckLog, 0, len(refs))
	var refused []string
	for _, ref := range refs {
		log := reported[ref.Key]
		text, cut := ledger.CIExcerpt(log.Text, int(settings.LogExcerptLines), int(settings.LogExcerptBytes))
		entry := ledger.CICheckLog{Check: ref.Name, URL: ref.URL, Key: ref.Key, Text: text}
		switch {
		case log.Refused:
			entry.Text, entry.Refused = "", true
			entry.Note = "the run its address names is not a run of this head, so its log was not read"
			refused = append(refused, ledger.CIOneLine(ref.Name, 60))
		case text == "":
			entry.Note = "no log could be read of it"
		case cut:
			entry.Note = "the end of a longer log"
		}
		kept = append(kept, entry)
	}
	body, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	if _, err := tx.Forge().FinishRequestWith(request.ID, request.Attempt, tx.Now(), "", string(body)); err != nil {
		return err
	}
	if err := appendForgeAs(tx, id, at, ledger.EventCILogRead, request.WorkOrder, ciEvent{
		Head: request.Commit, Request: request.ID, Checks: int64(len(kept)), Refused: strings.Join(refused, ", "),
	}, actor); err != nil {
		return err
	}
	return integrate(tx, request.WorkOrder)
}
