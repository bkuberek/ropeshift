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

// PracticesRequestView is a request that a worker run the practices check, with the state it stands in at the time of
// the read: offered until its expiry passes, then expired, and reported once its worker reported it.
type PracticesRequestView = ledger.PracticesRequest

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

// PracticeFindingView is one finding of a result, with the table's words for the practice it belongs to.
type PracticeFindingView struct {
	practices.Finding
	PracticeGuide
}

// PracticesAcknowledgement is a member's reason for leaving a finding open, which the finding keeps showing beside.
type PracticesAcknowledgement struct {
	Finding        string    `json:"finding"`
	Reason         string    `json:"reason"`
	Member         string    `json:"member"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
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

// PracticesRequestViewOf reads a request as it stands at a time: offered past its expiry reads as expired.
func PracticesRequestViewOf(request ledger.PracticesRequest, now int64) PracticesRequestView {
	request.State = request.StateAt(now)
	return request
}

func guideOf(id practices.ID) PracticeGuide {
	practice, _ := practices.Lookup(id)
	return PracticeGuide{Why: practice.Why, Fix: practice.Fix, Template: practice.Template, Command: practice.Command()}
}

// PracticesViewOf reads a repository's latest result and open request, either of which may be nil, as a client
// reads them at a time.
func PracticesViewOf(repository ledger.Repository, record *ledger.PracticesRecord, open *ledger.PracticesRequest, now int64) PracticesView {
	view := PracticesView{Repository: repository.ID, Name: repository.Name}
	if open != nil {
		request := PracticesRequestViewOf(*open, now)
		view.Request = &request
	}
	if record == nil {
		return view
	}
	result := record.Result
	read := PracticesResultView{
		Request: record.Request, Commit: result.Commit, BaseBranch: result.BaseBranch, Worker: result.Worker, CheckedAt: result.CheckedAt,
		TableVersion: result.TableVersion, RulesVersion: result.RulesVersion, ScannedFrom: result.ScannedFrom, Skipped: result.Skipped, Truncated: result.Truncated,
		Practices: make([]PracticeView, 0, len(result.Practices)), Findings: make([]PracticeFindingView, 0, len(result.Findings)),
	}
	for _, status := range result.Practices {
		practice, _ := practices.Lookup(status.ID)
		read.Practices = append(read.Practices, PracticeView{ID: status.ID, Severity: practice.Severity, Status: status.Status, Reason: status.Reason, PracticeGuide: guideOf(status.ID)})
	}
	for _, finding := range result.Findings {
		read.Findings = append(read.Findings, PracticeFindingView{Finding: finding, PracticeGuide: guideOf(finding.Practice)})
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
