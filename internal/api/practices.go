package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/practices"
)

// PracticesPath reads the practices check's latest result of each repository, and PracticesChecksPath asks that the
// check run, and reads a request by its ID after it.
const (
	PracticesPath       = "/v1/practices"
	PracticesChecksPath = "/v1/practices/checks"
)

// PracticesAcknowledgementsPath acknowledges a finding, and PracticesWithdrawPath withdraws the acknowledgement.
const (
	PracticesAcknowledgementsPath = "/v1/practices/acknowledgements"
	PracticesWithdrawPath         = "/v1/practices/acknowledgements/withdraw"
)

// PracticesRequestView is a request that a worker run the practices check, with the state it stands in at the time of
// the read: offered until its expiry passes, then expired, and reported once its worker reported it. WorkerName is
// the name of the worker it was asked of, beside the worker's ID.
type PracticesRequestView struct {
	ledger.PracticesRequest
	WorkerName string `json:"worker_name,omitempty"`
}

// PracticeGuide is what the practice table says of a practice: why it matters, how to meet it, and the template that
// supplies the file it asks for. Command prints the template, and is empty for a practice with none.
type PracticeGuide struct {
	Why      string `json:"why"`
	Fix      string `json:"fix"`
	Template string `json:"template,omitempty"`
	Command  string `json:"command,omitempty"`
}

// PracticeView is one practice's status in a result, with its severity and the table's words for it.
type PracticeView struct {
	ID       practices.ID       `json:"id"`
	Severity practices.Severity `json:"severity"`
	Status   practices.Status   `json:"status"`
	// Reason says why a practice is unknown or not applicable.
	Reason string `json:"reason,omitempty"`
	PracticeGuide
}

// PracticeFindingView is one finding of a result, with the table's words for the practice it belongs to. A finding a
// member acknowledged stays in the result and reads Acknowledged, with the acknowledgement: its practice is still a
// finding.
type PracticeFindingView struct {
	practices.Finding
	PracticeGuide
	Acknowledged    bool                      `json:"acknowledged,omitempty"`
	Acknowledgement *PracticesAcknowledgement `json:"acknowledgement,omitempty"`
}

// PracticesAcknowledgement is a member's reason for leaving a finding open, which the finding keeps showing beside.
type PracticesAcknowledgement struct {
	Finding        string    `json:"finding"`
	Reason         string    `json:"reason"`
	Member         string    `json:"member"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}

// PracticesAcknowledge acknowledges a repository's finding, by the repository's name or ID, with the reason for
// leaving it open: at least a character, and at most ledger.MaxPracticesReason bytes.
type PracticesAcknowledge struct {
	Repository string `json:"repository"`
	Finding    string `json:"finding"`
	Reason     string `json:"reason"`
}

// PracticesWithdraw withdraws the acknowledgement of a repository's finding.
type PracticesWithdraw struct {
	Repository string `json:"repository"`
	Finding    string `json:"finding"`
}

// PracticesAcknowledgementOf reads an acknowledgement as a client reads it.
func PracticesAcknowledgementOf(ack ledger.PracticesAcknowledgement) PracticesAcknowledgement {
	return PracticesAcknowledgement{Finding: ack.Finding, Reason: ack.Reason, Member: ack.Member, AcknowledgedAt: time.UnixMilli(ack.AcknowledgedAt).UTC()}
}

// PracticesResultView is what one check of a repository came to.
type PracticesResultView struct {
	// Request is the request whose report made this the repository's latest result.
	Request      string    `json:"request"`
	Commit       string    `json:"commit"`
	BaseBranch   string    `json:"base_branch"`
	Worker       string    `json:"worker"`
	CheckedAt    time.Time `json:"checked_at"`
	TableVersion int       `json:"table_version"`
	RulesVersion int       `json:"rules_version"`
	// ScannedFrom is the commit the secret scan started from, empty for a scan of the whole history.
	ScannedFrom string `json:"scanned_from,omitempty"`
	// Skipped counts the files the secret scan skipped for their size or binary content.
	Skipped int `json:"skipped,omitempty"`
	// Partial says why the secret scan read less history than the repository has, absent for a scan of all of it.
	Partial string `json:"partial,omitempty"`
	// Truncated says secret findings beyond the bound were left out.
	Truncated bool `json:"truncated,omitempty"`
	// Practices holds one status per practice, in table order, and Findings are sorted by severity.
	Practices []PracticeView        `json:"practices"`
	Findings  []PracticeFindingView `json:"findings"`
}

// PracticesView is one repository as the practices check reads it: its latest result, and the request offered for it
// that has not expired.
type PracticesView struct {
	Repository string `json:"repository"`
	Name       string `json:"name"`
	// Result is the latest result, and absent before a check reported.
	Result *PracticesResultView `json:"result,omitempty"`
	// Request is the open request, if any.
	Request *PracticesRequestView `json:"request,omitempty"`
	// Acknowledgement is each finding a member acknowledged, with the reason.
	Acknowledgement []PracticesAcknowledgement `json:"acknowledgement,omitempty"`
}

// PracticesList is the practices of each repository asked for, by name.
type PracticesList struct {
	Repositories []PracticesView `json:"repositories"`
}

// PracticesCheck asks that the practices check run on the repositories named, by name or ID, or on every active
// repository when none is.
type PracticesCheck struct {
	Repositories []string `json:"repositories"`
}

// PracticesCheckEntry is what became of one repository in a request: the request that was made or found open, or why
// none was.
type PracticesCheckEntry struct {
	Repository string `json:"repository"`
	Name       string `json:"name"`
	// Request is the request offered for the repository, and absent when none could be made.
	Request *PracticesRequestView `json:"request,omitempty"`
	// Existing says the repository already had this request open, so none was made.
	Existing bool `json:"existing,omitempty"`
	// Reason says why no request was made.
	Reason string `json:"reason,omitempty"`
}

// PracticesChecked is one entry per repository a request named, in the order named.
type PracticesChecked struct {
	Checks []PracticesCheckEntry `json:"checks"`
}

// PracticesRequestViewOf reads a request as it stands at a time: offered past its expiry reads as expired. workers
// names each worker by its ID, and a worker it does not name reads with no name.
func PracticesRequestViewOf(request ledger.PracticesRequest, now int64, workers map[string]string) PracticesRequestView {
	request.State = request.StateAt(now)
	return PracticesRequestView{PracticesRequest: request, WorkerName: workers[request.Worker]}
}

func guideOf(id practices.ID) PracticeGuide {
	practice, _ := practices.Lookup(id)
	return PracticeGuide{Why: practice.Why, Fix: practice.Fix, Template: practice.Template, Command: practice.Command()}
}

// PracticesResultViewOf reads a result as a client reads it, with the table's words for each practice and finding and
// no acknowledgement. Request is the request whose report made it a repository's latest result, empty for a result no
// request made.
func PracticesResultViewOf(request string, result practices.Result) PracticesResultView {
	read := PracticesResultView{
		Request: request, Commit: result.Commit, BaseBranch: result.BaseBranch, Worker: result.Worker, CheckedAt: result.CheckedAt,
		TableVersion: result.TableVersion, RulesVersion: result.RulesVersion, ScannedFrom: result.ScannedFrom, Skipped: result.Skipped, Partial: result.Partial, Truncated: result.Truncated,
		Practices: make([]PracticeView, 0, len(result.Practices)), Findings: make([]PracticeFindingView, 0, len(result.Findings)),
	}
	for _, status := range result.Practices {
		practice, _ := practices.Lookup(status.ID)
		read.Practices = append(read.Practices, PracticeView{ID: status.ID, Severity: practice.Severity, Status: status.Status, Reason: status.Reason, PracticeGuide: guideOf(status.ID)})
	}
	for _, finding := range result.Findings {
		read.Findings = append(read.Findings, PracticeFindingView{Finding: finding, PracticeGuide: guideOf(finding.Practice)})
	}
	return read
}

// PracticesViewOf reads a repository's latest result and open request, either of which may be nil, and its
// acknowledgements, as a client reads them at a time, with the workers named as PracticesRequestViewOf names them.
// Every finding of the result stays in its list: one a member acknowledged reads Acknowledged, with the acknowledgement.
func PracticesViewOf(repository ledger.Repository, record *ledger.PracticesRecord, open *ledger.PracticesRequest, acknowledged []ledger.PracticesAcknowledgement, now int64, workers map[string]string) PracticesView {
	view := PracticesView{Repository: repository.ID, Name: repository.Name}
	acks := make(map[string]PracticesAcknowledgement, len(acknowledged))
	for _, ack := range acknowledged {
		read := PracticesAcknowledgementOf(ack)
		acks[ack.Finding] = read
		view.Acknowledgement = append(view.Acknowledgement, read)
	}
	if open != nil {
		request := PracticesRequestViewOf(*open, now, workers)
		view.Request = &request
	}
	if record == nil {
		return view
	}
	read := PracticesResultViewOf(record.Request, record.Result)
	for i, finding := range read.Findings {
		if ack, ok := acks[finding.ID]; ok {
			read.Findings[i].Acknowledged, read.Findings[i].Acknowledgement = true, &ack
		}
	}
	view.Result = &read
	return view
}

// Practices reads the practices of the repository named, by name or ID, or of every active repository when none is.
func (c *ProjectClient) Practices(ctx context.Context, repository string) (PracticesList, error) {
	var out PracticesList
	path := PracticesPath
	if repository != "" {
		path += "?" + url.Values{"repository": {repository}}.Encode()
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// CheckPractices asks that the check run on the repositories named, or on every active repository when none is.
func (c *ProjectClient) CheckPractices(ctx context.Context, repositories []string) (PracticesChecked, error) {
	var out PracticesChecked
	if repositories == nil {
		repositories = []string{}
	}
	err := c.do(ctx, http.MethodPost, PracticesChecksPath, PracticesCheck{Repositories: repositories}, &out)
	return out, err
}

// PracticesRequest reads one request, and the state it stands in.
func (c *ProjectClient) PracticesRequest(ctx context.Context, id string) (PracticesRequestView, error) {
	var out PracticesRequestView
	err := c.do(ctx, http.MethodGet, PracticesChecksPath+"/"+url.PathEscape(id), nil, &out)
	return out, err
}

// AcknowledgePractices acknowledges a repository's finding with a reason, which only a member can do: a delegate's call
// is a card a member confirms.
func (c *ProjectClient) AcknowledgePractices(ctx context.Context, in PracticesAcknowledge) (PracticesAcknowledgement, error) {
	var out PracticesAcknowledgement
	err := c.do(ctx, http.MethodPost, PracticesAcknowledgementsPath, in, &out)
	return out, err
}

// WithdrawPractices withdraws the acknowledgement of a repository's finding, and answers the acknowledgement it
// removed.
func (c *ProjectClient) WithdrawPractices(ctx context.Context, in PracticesWithdraw) (PracticesAcknowledgement, error) {
	var out PracticesAcknowledgement
	err := c.do(ctx, http.MethodPost, PracticesWithdrawPath, in, &out)
	return out, err
}
