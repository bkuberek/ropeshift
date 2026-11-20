package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/questions"
)

// RecordFilter narrows questions or decisions. An empty field does not filter.
type RecordFilter struct {
	Category string
	Status   string
	Scope    string
	Text     string
}

func (f RecordFilter) query() string {
	values := url.Values{}
	for name, value := range map[string]string{"category": f.Category, "status": f.Status, "scope": f.Scope, "text": f.Text} {
		if value != "" {
			values.Set(name, value)
		}
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// QuestionList is the answer to a list of questions.
type QuestionList struct {
	Questions []QuestionView `json:"questions"`
}

// QuestionView is a question as a read answers it. Scope is the scope an answer to it takes, from the record it is
// about (questions.ScopeOf), and ScopeTitle the title of that record, as DecisionView says. Reversibility is what an
// answer to it will be, by the rule an item of a batch states it by (questions.Policy.AnswerReversibility), so no
// surface that answers a question chooses a one-way answer for a member.
//
// The rest show a member the whole ladder (FR-QD-13). Holder is who holds the question while the hold lasts, and is
// absent when nobody does. Machines names the machine of each orchestrator the ladder trace, the hold, or the closing
// decision names, by its worker's ID, which the ID does not say. Attached is every lane that asked the question or waits
// on it. Batch is the batch that gathered it, ClosedBy the decision or assumption that answered it, and MergedInto the
// question that took its place when it was merged.
type QuestionView struct {
	questions.Question
	Scope         string                 `json:"scope,omitempty"`
	ScopeTitle    string                 `json:"scope_title,omitempty"`
	Reversibility string                 `json:"reversibility,omitempty"`
	Holder        *QuestionHolder        `json:"holder,omitempty"`
	Machines      map[string]string      `json:"machines,omitempty"`
	Attached      []questions.Attachment `json:"attached,omitempty"`
	Batch         *questions.Batch       `json:"batch,omitempty"`
	ClosedBy      *questions.Decision    `json:"closed_by,omitempty"`
	MergedInto    string                 `json:"merged_into,omitempty"`
}

// QuestionHolder is who holds a question and until when. An orchestrator holds as its worker, so its hold names the
// orchestrator and the machine it runs on; a member's names neither.
type QuestionHolder struct {
	ID           string `json:"id"`
	Orchestrator string `json:"orchestrator,omitempty"`
	Machine      string `json:"machine,omitempty"`
	Until        int64  `json:"until"`
}

// The statuses the questions view takes beside a question's own: every question, and the questions closed by a merge.
// With no status the view holds the questions still open, at every rung.
const (
	QuestionStatusAll    = "all"
	QuestionStatusMerged = "merged"
)

// QuestionFilter narrows the questions view. Status is a question's status, `merged`, or `all`, and the open questions
// when empty, so a merged question reads as merged and not as dismissed. Lane keeps the questions a lane asked or is
// attached to, and WorkOrder those about a work order or asked by one of its lanes.
type QuestionFilter struct {
	Category  string
	Status    string
	Lane      string
	WorkOrder string
}

func (f QuestionFilter) query() string {
	values := url.Values{}
	for name, value := range map[string]string{"category": f.Category, "status": f.Status, "lane": f.Lane, "work_order": f.WorkOrder} {
		if value != "" {
			values.Set(name, value)
		}
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// ReadQuestions reads the questions view a filter narrows.
func (c *ProjectClient) ReadQuestions(ctx context.Context, filter QuestionFilter) (Questions, error) {
	var out Questions
	err := c.do(ctx, http.MethodGet, ViewPath(ViewQuestions)+filter.query(), nil, &out)
	return out, err
}

// QuestionBatch is a delivered batch as a read answers it: what still waits for a member in it.
type QuestionBatch struct {
	Batch questions.Batch `json:"batch"`
	Items []QuestionItem  `json:"items"`
}

// QuestionItem is one question of a batch, with the title of the record its scope names, as DecisionView says, and the
// lane that asked it, which is empty when no lane the ledger holds did, as for a member's question.
type QuestionItem struct {
	questions.Item
	ScopeTitle string `json:"scope_title,omitempty"`
	Lane       string `json:"lane,omitempty"`
}

// DecisionList is the answer to a list of decisions.
type DecisionList struct {
	Decisions []DecisionView `json:"decisions"`
}

// DecisionView is a decision as a read answers it. ScopeTitle is the title of the record its scope names, read in
// the same read: a plan's, a contract's newest version's, or a work order's title, or a repository's name. It is
// empty when the ledger holds no such record, for a scope written before the grammar, and for a title that only
// repeats the scope's ID, so a display shows the scope alone.
type DecisionView struct {
	questions.Decision
	ScopeTitle string `json:"scope_title,omitempty"`
}

// ScopeLabel is how a client names a scope: the scope, and beside it the title of the record it names when the
// read gave one, as questions.ScopeLabel says.
func ScopeLabel(scope, title string) string {
	return questions.ScopeLabel(scope, title)
}

// RecordQuestion asks for a question to be recorded. The client generates ID, so a retry
// returns the first answer.
type RecordQuestion struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	Categories  []string `json:"categories,omitempty"`
	Blocking    bool     `json:"blocking,omitempty"`
	SubjectKind string   `json:"subject_kind,omitempty"`
	SubjectID   string   `json:"subject_id,omitempty"`
}

// RecordDecision asks for a decision to be recorded. The client generates ID.
type RecordDecision struct {
	ID            string                   `json:"id"`
	Status        questions.DecisionStatus `json:"status"`
	Statement     string                   `json:"statement"`
	Rationale     string                   `json:"rationale,omitempty"`
	Citations     []string                 `json:"citations,omitempty"`
	Scope         string                   `json:"scope,omitempty"`
	Reversibility string                   `json:"reversibility,omitempty"`
	Categories    []string                 `json:"categories,omitempty"`
	Supersedes    string                   `json:"supersedes,omitempty"`
	QuestionID    string                   `json:"question_id,omitempty"`
	NoConflict    []string                 `json:"no_conflict,omitempty"`
}

// OverturnDecision is a member's reversal. ID names the decision that takes its place, and
// the client generates it, so a retry finds what the first attempt wrote.
type OverturnDecision struct {
	ID        string   `json:"id"`
	Statement string   `json:"statement"`
	Rationale string   `json:"rationale,omitempty"`
	Citations []string `json:"citations,omitempty"`
}

// PolicyList is every version of the decision policy, newest first.
type PolicyList struct {
	Versions []authority.PolicyVersion `json:"versions"`
}

// PublishPolicy is a new version of the decision policy, as its table is written.
type PublishPolicy struct {
	Body string `json:"body"`
}

// PublishedPolicy is a policy version as published, and a notice of what is still to follow from it.
type PublishedPolicy struct {
	authority.PolicyVersion
	Notice string `json:"notice,omitempty"`
}

// NoticeTakeOutPending says an estimate policy that stops sharing history is in force, and the project's records
// are still in the installation's pool until the next delivery tick takes them out.
const NoticeTakeOutPending = "The policy is in force, and the project's delivery records are still in the installation's pool: the next delivery tick takes them out."

// AnswerQuestion is a member's answer. ID names the decision it becomes, and the client
// generates it, so a retry finds what the first attempt wrote. Supersedes names the decision
// in force the answer replaces, in the transaction that records it, and NoConflict the decisions
// the answer's scope overlaps that it does not conflict with.
type AnswerQuestion struct {
	ID         string   `json:"id"`
	Answer     string   `json:"answer"`
	Rationale  string   `json:"rationale,omitempty"`
	Citations  []string `json:"citations,omitempty"`
	Supersedes string   `json:"supersedes,omitempty"`
	NoConflict []string `json:"no_conflict,omitempty"`
	// Place is where the member answered: in Needs you, or inline in a conversation.
	Place string `json:"place,omitempty"`
	// Assumed records the answer as an assumption, for a question whose answer is reversible.
	Assumed bool `json:"assumed,omitempty"`
}

// MergeQuestion names the question that stays when a member merges one into it.
type MergeQuestion struct {
	Into string `json:"into"`
}

// BatchAnswerItem is a member's answer to one question of a batch. ID names the decision it becomes. Accepted takes
// the question's recommendation, with its rationale and the item's citations, and then the answer, rationale, and
// citations stay empty; any other answer is the member's own, and says why. Supersedes and NoConflict mean what they
// do for one answer.
type BatchAnswerItem struct {
	Question   string   `json:"question"`
	ID         string   `json:"id"`
	Answer     string   `json:"answer,omitempty"`
	Rationale  string   `json:"rationale,omitempty"`
	Citations  []string `json:"citations,omitempty"`
	Supersedes string   `json:"supersedes,omitempty"`
	NoConflict []string `json:"no_conflict,omitempty"`
	Accepted   bool     `json:"accepted,omitempty"`
	// ExpectReversibility is the reversibility the item stated when the member read it, `reversible` or `one_way`.
	// The request is refused when the policy in force now says otherwise. Empty takes what the policy says.
	ExpectReversibility string `json:"expect_reversibility,omitempty"`
}

// AnswerBatch answers a batch's questions in one request, which records every answer or none, and names the first
// one it refuses. Place is where the member answered, as for one answer.
type AnswerBatch struct {
	Answers []BatchAnswerItem `json:"answers"`
	Place   string            `json:"place,omitempty"`
}

// BatchAnswered is the decisions a batch's answers became, in the order of the request.
type BatchAnswered struct {
	Decisions []questions.Decision `json:"decisions"`
}

// BatchList is what reached members and still waits for them.
type BatchList struct {
	Batches []QuestionBatch `json:"batches"`
}

// CategoryList is every category of a workspace, built-in and added.
type CategoryList struct {
	Categories []string `json:"categories"`
}

// AddCategory extends a workspace's categories.
type AddCategory struct {
	Name string `json:"name"`
}

// Candidate is one record a question may match.
type Candidate struct {
	Kind     string  `json:"kind"`
	RecordID string  `json:"record_id"`
	Score    float64 `json:"score"`
}

// CandidateList is what a search found, best first.
type CandidateList struct {
	Candidates []Candidate `json:"candidates"`
}

// Questions lists the questions a filter selects.
func (c *ProjectClient) Questions(ctx context.Context, filter RecordFilter) ([]QuestionView, error) {
	var out QuestionList
	err := c.do(ctx, http.MethodGet, "/v1/questions"+filter.query(), nil, &out)
	return out.Questions, err
}

// Question reads one question.
func (c *ProjectClient) Question(ctx context.Context, id string) (QuestionView, error) {
	var out QuestionView
	err := c.do(ctx, http.MethodGet, "/v1/questions/"+url.PathEscape(id), nil, &out)
	return out, err
}

// RecordQuestion records a question as it is.
func (c *ProjectClient) RecordQuestion(ctx context.Context, in RecordQuestion) (questions.Question, error) {
	var out questions.Question
	err := c.do(ctx, http.MethodPost, "/v1/questions", in, &out)
	return out, err
}

// DismissQuestion closes a question with no answer.
func (c *ProjectClient) DismissQuestion(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/questions/"+url.PathEscape(id)+"/dismiss", struct{}{}, nil)
}

// MergeQuestion closes a question in favor of the one that stays, which takes its lanes.
func (c *ProjectClient) MergeQuestion(ctx context.Context, id string, in MergeQuestion) error {
	return c.do(ctx, http.MethodPost, "/v1/questions/"+url.PathEscape(id)+"/merge", in, nil)
}

// AnswerQuestion closes a question with a member's answer.
func (c *ProjectClient) AnswerQuestion(ctx context.Context, id string, in AnswerQuestion) (questions.Decision, error) {
	var out questions.Decision
	err := c.do(ctx, http.MethodPost, "/v1/questions/"+url.PathEscape(id)+"/answer", in, &out)
	return out, err
}

// AnswerBatch answers the questions of one batch in one request, and returns the decisions in the order asked.
func (c *ProjectClient) AnswerBatch(ctx context.Context, batch string, in AnswerBatch) ([]questions.Decision, error) {
	var out BatchAnswered
	err := c.do(ctx, http.MethodPost, "/v1/batches/"+url.PathEscape(batch)+"/answers", in, &out)
	return out.Decisions, err
}

// Batches lists the delivered batches that still wait for a member, within one category.
// An empty category lists them all.
func (c *ProjectClient) Batches(ctx context.Context, category string) ([]QuestionBatch, error) {
	path := "/v1/question-batches"
	if category != "" {
		path += "?category=" + url.QueryEscape(category)
	}
	var out BatchList
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out.Batches, err
}

// Decisions lists the decisions a filter selects.
func (c *ProjectClient) Decisions(ctx context.Context, filter RecordFilter) ([]DecisionView, error) {
	var out DecisionList
	err := c.do(ctx, http.MethodGet, "/v1/decisions"+filter.query(), nil, &out)
	return out.Decisions, err
}

// Decision reads one decision.
func (c *ProjectClient) Decision(ctx context.Context, id string) (DecisionView, error) {
	var out DecisionView
	err := c.do(ctx, http.MethodGet, "/v1/decisions/"+url.PathEscape(id), nil, &out)
	return out, err
}

// RecordDecision records a decision.
func (c *ProjectClient) RecordDecision(ctx context.Context, in RecordDecision) (questions.Decision, error) {
	var out questions.Decision
	err := c.do(ctx, http.MethodPost, "/v1/decisions", in, &out)
	return out, err
}

// OverturnDecision reverses a decision, with what takes its place.
func (c *ProjectClient) OverturnDecision(ctx context.Context, id string, in OverturnDecision) (questions.Overturned, error) {
	var out questions.Overturned
	err := c.do(ctx, http.MethodPost, "/v1/decisions/"+url.PathEscape(id)+"/overturn", in, &out)
	return out, err
}

// Policies lists the versions of the decision policy.
func (c *ProjectClient) Policies(ctx context.Context) ([]authority.PolicyVersion, error) {
	var out PolicyList
	err := c.do(ctx, http.MethodGet, "/v1/policies", nil, &out)
	return out.Versions, err
}

// Policy reads one version of the decision policy.
func (c *ProjectClient) Policy(ctx context.Context, version int64) (authority.PolicyVersion, error) {
	var out authority.PolicyVersion
	err := c.do(ctx, http.MethodGet, "/v1/policies/"+strconv.FormatInt(version, 10), nil, &out)
	return out, err
}

// LanePolicies lists the versions of the lane policy, newest first.
func (c *ProjectClient) LanePolicies(ctx context.Context) ([]authority.PolicyVersion, error) {
	var out PolicyList
	err := c.do(ctx, http.MethodGet, "/v1/policies?kind="+string(authority.PolicyLane), nil, &out)
	return out.Versions, err
}

// LanePolicy reads one version of the lane policy.
func (c *ProjectClient) LanePolicy(ctx context.Context, version int64) (authority.PolicyVersion, error) {
	var out authority.PolicyVersion
	err := c.do(ctx, http.MethodGet, "/v1/policies/"+strconv.FormatInt(version, 10)+"?kind="+string(authority.PolicyLane), nil, &out)
	return out, err
}

// PoliciesOf lists the versions of one kind of policy, newest first.
func (c *ProjectClient) PoliciesOf(ctx context.Context, kind authority.PolicyKind) ([]authority.PolicyVersion, error) {
	var out PolicyList
	err := c.do(ctx, http.MethodGet, "/v1/policies?kind="+string(kind), nil, &out)
	return out.Versions, err
}

// PolicyOf reads one version of one kind of policy.
func (c *ProjectClient) PolicyOf(ctx context.Context, kind authority.PolicyKind, version int64) (authority.PolicyVersion, error) {
	var out authority.PolicyVersion
	err := c.do(ctx, http.MethodGet, "/v1/policies/"+strconv.FormatInt(version, 10)+"?kind="+string(kind), nil, &out)
	return out, err
}

// PublishPolicy records a new version of the policy its body's table names.
func (c *ProjectClient) PublishPolicy(ctx context.Context, body string) (PublishedPolicy, error) {
	var out PublishedPolicy
	err := c.do(ctx, http.MethodPost, "/v1/policies", PublishPolicy{Body: body}, &out)
	return out, err
}

// Categories lists every category of the workspace.
func (c *ProjectClient) Categories(ctx context.Context) ([]string, error) {
	var out CategoryList
	err := c.do(ctx, http.MethodGet, "/v1/categories", nil, &out)
	return out.Categories, err
}

// AddCategory extends the workspace's categories.
func (c *ProjectClient) AddCategory(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, "/v1/categories", AddCategory{Name: name}, nil)
}

// Candidates reads ranked candidates of some kinds of record.
func (c *ProjectClient) Candidates(ctx context.Context, text string, kinds []string, limit int) ([]Candidate, error) {
	values := url.Values{"text": {text}, "kinds": {strings.Join(kinds, ",")}}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	var out CandidateList
	err := c.do(ctx, http.MethodGet, "/v1/candidates?"+values.Encode(), nil, &out)
	return out.Candidates, err
}
