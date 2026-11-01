package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"ropeshift.dev/ropeshift/internal/questions"
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
func (c *Client) Questions(ctx context.Context, filter RecordFilter) ([]questions.Question, error) {
	var out QuestionList
	err := c.Do(ctx, http.MethodGet, "/v1/questions"+filter.query(), nil, &out)
	return out.Questions, err
}

// Question reads one question.
func (c *Client) Question(ctx context.Context, id string) (questions.Question, error) {
	var out questions.Question
	err := c.Do(ctx, http.MethodGet, "/v1/questions/"+url.PathEscape(id), nil, &out)
	return out, err
}

// RecordQuestion records a question as it is.
func (c *Client) RecordQuestion(ctx context.Context, in RecordQuestion) (questions.Question, error) {
	var out questions.Question
	err := c.Do(ctx, http.MethodPost, "/v1/questions", in, &out)
	return out, err
}

// DismissQuestion closes a question with no answer.
func (c *Client) DismissQuestion(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodPost, "/v1/questions/"+url.PathEscape(id)+"/dismiss", struct{}{}, nil)
}

// Decisions lists the decisions a filter selects.
func (c *Client) Decisions(ctx context.Context, filter RecordFilter) ([]questions.Decision, error) {
	var out DecisionList
	err := c.Do(ctx, http.MethodGet, "/v1/decisions"+filter.query(), nil, &out)
	return out.Decisions, err
}

// Decision reads one decision.
func (c *Client) Decision(ctx context.Context, id string) (questions.Decision, error) {
	var out questions.Decision
	err := c.Do(ctx, http.MethodGet, "/v1/decisions/"+url.PathEscape(id), nil, &out)
	return out, err
}

// RecordDecision records a decision.
func (c *Client) RecordDecision(ctx context.Context, in RecordDecision) (questions.Decision, error) {
	var out questions.Decision
	err := c.Do(ctx, http.MethodPost, "/v1/decisions", in, &out)
	return out, err
}

// Categories lists every category of the workspace.
func (c *Client) Categories(ctx context.Context) ([]string, error) {
	var out CategoryList
	err := c.Do(ctx, http.MethodGet, "/v1/categories", nil, &out)
	return out.Categories, err
}

// AddCategory extends the workspace's categories.
func (c *Client) AddCategory(ctx context.Context, name string) error {
	return c.Do(ctx, http.MethodPost, "/v1/categories", AddCategory{Name: name}, nil)
}

// Candidates reads ranked candidates of some kinds of record.
func (c *Client) Candidates(ctx context.Context, text string, kinds []string, limit int) ([]Candidate, error) {
	values := url.Values{"text": {text}, "kinds": {strings.Join(kinds, ",")}}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	var out CandidateList
	err := c.Do(ctx, http.MethodGet, "/v1/candidates?"+values.Encode(), nil, &out)
	return out.Candidates, err
}
