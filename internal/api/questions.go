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
	Questions []questions.Question `json:"questions"`
}

// DecisionList is the answer to a list of decisions.
type DecisionList struct {
	Decisions []questions.Decision `json:"decisions"`
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

// AnswerQuestion is a member's answer. ID names the decision it becomes, and the client
// generates it, so a retry finds what the first attempt wrote.
type AnswerQuestion struct {
	ID        string   `json:"id"`
	Answer    string   `json:"answer"`
	Rationale string   `json:"rationale,omitempty"`
	Citations []string `json:"citations,omitempty"`
	// Place is where the member answered: in Needs you, or inline in a conversation.
	Place string `json:"place,omitempty"`
}

// BatchList is what reached members and still waits for them.
type BatchList struct {
	Batches []questions.Presented `json:"batches"`
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
func (c *ProjectClient) Questions(ctx context.Context, filter RecordFilter) ([]questions.Question, error) {
	var out QuestionList
	err := c.do(ctx, http.MethodGet, "/v1/questions"+filter.query(), nil, &out)
	return out.Questions, err
}

// Question reads one question.
func (c *ProjectClient) Question(ctx context.Context, id string) (questions.Question, error) {
	var out questions.Question
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

// AnswerQuestion closes a question with a member's answer.
func (c *ProjectClient) AnswerQuestion(ctx context.Context, id string, in AnswerQuestion) (questions.Decision, error) {
	var out questions.Decision
	err := c.do(ctx, http.MethodPost, "/v1/questions/"+url.PathEscape(id)+"/answer", in, &out)
	return out, err
}

// Batches lists the delivered batches that still wait for a member, within one category.
// An empty category lists them all.
func (c *ProjectClient) Batches(ctx context.Context, category string) ([]questions.Presented, error) {
	path := "/v1/question-batches"
	if category != "" {
		path += "?category=" + url.QueryEscape(category)
	}
	var out BatchList
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out.Batches, err
}

// Decisions lists the decisions a filter selects.
func (c *ProjectClient) Decisions(ctx context.Context, filter RecordFilter) ([]questions.Decision, error) {
	var out DecisionList
	err := c.do(ctx, http.MethodGet, "/v1/decisions"+filter.query(), nil, &out)
	return out.Decisions, err
}

// Decision reads one decision.
func (c *ProjectClient) Decision(ctx context.Context, id string) (questions.Decision, error) {
	var out questions.Decision
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
func (c *ProjectClient) PublishPolicy(ctx context.Context, body string) (authority.PolicyVersion, error) {
	var out authority.PolicyVersion
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
