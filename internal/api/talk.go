package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/questions"
)

// The refusals of the conversation surface.
const (
	// CodeNoAssistantWorker is a start of the assistant's session with no worker able to run it.
	CodeNoAssistantWorker = "no_assistant_worker"
	// CodeTargetNotRunning is a request a target's session must be running for: a compaction or a clear of a
	// stopped conversation, or a message to an orchestrator that is not running.
	CodeTargetNotRunning = "target_not_running"
)

// The paths of the conversation surface, under a project's routes.
const (
	ConversationsPath       = "/v1/conversations"
	ConversationTargetsPath = "/v1/conversation-targets"
)

// The bounds of a page of a stream.
const (
	TalkDefaultLimit = 100
	TalkMaxLimit     = 500
)

// The places a decision is made in.
const (
	PlaceNeedsYou     = "needs_you"
	PlaceConversation = "conversation"
)

// AssistantWorker is one worker and whether it can run the project's assistant, and why not.
type AssistantWorker struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Conversation is one of a member's conversations with the assistant, with the cards it holds open and the
// worker it is placed on, by name.
type Conversation struct {
	conversations.Conversation
	WorkerName string `json:"worker_name,omitempty"`
	OpenCards  int    `json:"open_cards"`
}

// ConversationList is the calling member's conversations, newest activity first, and each worker with
// whether it can run the project's assistant.
type ConversationList struct {
	AsOf          int64             `json:"as_of"`
	Conversations []Conversation    `json:"conversations"`
	Workers       []AssistantWorker `json:"workers"`
}

// CreateConversation starts a new conversation. ID is the client's, so a retry makes one.
type CreateConversation struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Worker string `json:"worker,omitempty"`
}

// OpenConversation starts a conversation's session on a worker, by name or ID, or on the one the
// placement rules choose.
type OpenConversation struct {
	Worker string `json:"worker,omitempty"`
}

// RenameConversation names a conversation.
type RenameConversation struct {
	Name string `json:"name"`
}

// ConversationMessage is a member's message to the assistant. ID is the client's, so a retry sends once.
type ConversationMessage struct {
	ID     string `json:"id"`
	Body   string `json:"body"`
	Worker string `json:"worker,omitempty"`
}

// ConversationOpened is a conversation an act started, and the running ones it stopped first.
type ConversationOpened struct {
	Conversation Conversation   `json:"conversation"`
	Stopped      []Conversation `json:"stopped"`
}

// ConversationSent is a member's message to the assistant as it was recorded.
type ConversationSent struct {
	Conversation Conversation     `json:"conversation"`
	Entry        TalkEntry        `json:"entry"`
	Message      messages.Message `json:"message"`
	Stopped      []Conversation   `json:"stopped"`
}

// TalkFrom is who an entry is from.
type TalkFrom struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	// DelegateKind is the kind of a delegate's credential, `external` or `assistant`.
	DelegateKind string `json:"delegate_kind,omitempty"`
}

// TalkQuestion is a question an entry holds, as it stands now, with the batch it reaches a person in.
type TalkQuestion struct {
	Question questions.Question `json:"question"`
	Batch    string             `json:"batch,omitempty"`
	// Channel is where a member's answer came from: the web listener, the command line, or the
	// conversation it showed in. It is empty until a member answers.
	Channel string `json:"channel,omitempty"`
}

// TalkFromAssistant is the kind of sender the assistant is in a conversation.
const TalkFromAssistant = "assistant"

// The kinds of a talk entry.
const (
	TalkKindMember   = "member"
	TalkKindReply    = "reply"
	TalkKindEvent    = "event"
	TalkKindAction   = "action"
	TalkKindCard     = "card"
	TalkKindRequest  = "request"
	TalkKindQuestion = "question"
	TalkKindMarker   = "marker"
)

// TalkEntry is one entry of a conversation, a lane's stream, or an orchestrator's stream, at the time it
// was recorded. A card, a request, and a question hold the record as it stands now, so one resolved
// anywhere shows its outcome in place.
type TalkEntry struct {
	ID          string                    `json:"id"`
	At          int64                     `json:"at"`
	Kind        string                    `json:"kind"`
	From        *TalkFrom                 `json:"from,omitempty"`
	Body        string                    `json:"body,omitempty"`
	Message     *messages.Message         `json:"message,omitempty"`
	Event       *StreamEvent              `json:"event,omitempty"`
	Action      *conversations.ActionData `json:"action,omitempty"`
	Approval    *authority.Approval       `json:"approval,omitempty"`
	Question    *TalkQuestion             `json:"question,omitempty"`
	Marker      string                    `json:"marker,omitempty"`
	Assent      bool                      `json:"assent,omitempty"`
	BeforeClear bool                      `json:"before_clear,omitempty"`
}

// TalkPage is a page of a stream: oldest first within the page, the newest page first. Next is what the
// next, older page's `before` takes, and empty when none is left.
type TalkPage struct {
	Entries []TalkEntry `json:"entries"`
	Next    string      `json:"next,omitempty"`
	AsOf    int64       `json:"as_of"`
}

// TalkQuery pages a stream.
type TalkQuery struct {
	Before string
	Limit  int
}

// Encode is the query a route reads.
func (q TalkQuery) Encode() string {
	values := url.Values{}
	if q.Before != "" {
		values.Set("before", q.Before)
	}
	if q.Limit > 0 {
		values.Set("limit", strconv.Itoa(q.Limit))
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// AssistantTarget is the assistant as a target: the member's newest conversation, and the workers.
type AssistantTarget struct {
	Conversation *Conversation     `json:"conversation"`
	Workers      []AssistantWorker `json:"workers"`
	IdleStopMs   int64             `json:"idle_stop_ms"`
	// WarmIdleStopMs is when a session that has had no message since it started stops.
	WarmIdleStopMs int64 `json:"warm_idle_stop_ms"`
}

// OrchestratorTarget is the orchestrator of a worker that serves the project.
type OrchestratorTarget struct {
	Worker     string   `json:"worker"`
	Name       string   `json:"name"`
	State      string   `json:"state"`
	Desired    string   `json:"desired_state"`
	Generation int64    `json:"generation"`
	Projects   []string `json:"projects"`
}

// SessionTarget is a lane a member can talk to: a planning session, or another lane that has not ended.
type SessionTarget struct {
	Lane       string `json:"lane"`
	WorkOrder  string `json:"work_order"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind"`
	Worker     string `json:"worker,omitempty"`
	WorkerName string `json:"worker_name,omitempty"`
	Generation int64  `json:"generation"`
	RunMode    string `json:"run_mode,omitempty"`
	State      string `json:"state"`
}

// ConversationTargets is what the calling member can talk to in a project now.
type ConversationTargets struct {
	AsOf          int64                `json:"as_of"`
	Assistant     AssistantTarget      `json:"assistant"`
	Orchestrators []OrchestratorTarget `json:"orchestrators"`
	Planning      []SessionTarget      `json:"planning"`
	Lanes         []SessionTarget      `json:"lanes"`
}

func conversationPath(id, verb string) string {
	path := ConversationsPath + "/" + url.PathEscape(id)
	if verb != "" {
		path += "/" + verb
	}
	return path
}

// Conversations lists the calling member's conversations and the workers that could run one.
func (c *ProjectClient) Conversations(ctx context.Context) (ConversationList, error) {
	var out ConversationList
	err := c.do(ctx, http.MethodGet, ConversationsPath, nil, &out)
	return out, err
}

// CreateConversation starts a new conversation.
func (c *ProjectClient) CreateConversation(ctx context.Context, in CreateConversation) (ConversationOpened, error) {
	var out ConversationOpened
	err := c.do(ctx, http.MethodPost, ConversationsPath, in, &out)
	return out, err
}

// Conversation reads one conversation.
func (c *ProjectClient) Conversation(ctx context.Context, id string) (Conversation, error) {
	var out Conversation
	err := c.do(ctx, http.MethodGet, conversationPath(id, ""), nil, &out)
	return out, err
}

// OpenConversation starts a conversation's session, resuming what it had.
func (c *ProjectClient) OpenConversation(ctx context.Context, id string, in OpenConversation) (ConversationOpened, error) {
	var out ConversationOpened
	err := c.do(ctx, http.MethodPost, conversationPath(id, "open"), in, &out)
	return out, err
}

// StopConversation stops a conversation's session.
func (c *ProjectClient) StopConversation(ctx context.Context, id string) (Conversation, error) {
	var out Conversation
	err := c.do(ctx, http.MethodPost, conversationPath(id, "stop"), nil, &out)
	return out, err
}

// RenameConversation names a conversation.
func (c *ProjectClient) RenameConversation(ctx context.Context, id, name string) (Conversation, error) {
	var out Conversation
	err := c.do(ctx, http.MethodPost, conversationPath(id, "rename"), RenameConversation{Name: name}, &out)
	return out, err
}

// DeleteConversation stops and removes a conversation with everything it holds.
func (c *ProjectClient) DeleteConversation(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, conversationPath(id, "delete"), nil, nil)
}

// CompactConversation asks a running conversation's session to compact its context.
func (c *ProjectClient) CompactConversation(ctx context.Context, id string) (Conversation, error) {
	var out Conversation
	err := c.do(ctx, http.MethodPost, conversationPath(id, "compact"), nil, &out)
	return out, err
}

// ClearConversation starts a running conversation's next generation with an empty context.
func (c *ProjectClient) ClearConversation(ctx context.Context, id string) (Conversation, error) {
	var out Conversation
	err := c.do(ctx, http.MethodPost, conversationPath(id, "clear"), nil, &out)
	return out, err
}

// ConversationEntries reads a page of a conversation.
func (c *ProjectClient) ConversationEntries(ctx context.Context, id string, q TalkQuery) (TalkPage, error) {
	var out TalkPage
	err := c.do(ctx, http.MethodGet, conversationPath(id, "entries")+q.Encode(), nil, &out)
	return out, err
}

// SendToAssistant sends a member's message to the assistant of a conversation.
func (c *ProjectClient) SendToAssistant(ctx context.Context, id string, in ConversationMessage) (ConversationSent, error) {
	var out ConversationSent
	err := c.do(ctx, http.MethodPost, conversationPath(id, "messages"), in, &out)
	return out, err
}

// ConversationTargets reads what the calling member can talk to.
func (c *ProjectClient) ConversationTargets(ctx context.Context) (ConversationTargets, error) {
	var out ConversationTargets
	err := c.do(ctx, http.MethodGet, ConversationTargetsPath, nil, &out)
	return out, err
}

// LaneStream reads a page of a lane's stream.
func (c *ProjectClient) LaneStream(ctx context.Context, lane string, q TalkQuery) (TalkPage, error) {
	var out TalkPage
	err := c.do(ctx, http.MethodGet, "/v1/lanes/"+url.PathEscape(lane)+"/stream"+q.Encode(), nil, &out)
	return out, err
}

// OrchestratorStream reads a page of a worker's orchestrator's stream in this project.
func (c *ProjectClient) OrchestratorStream(ctx context.Context, worker string, q TalkQuery) (TalkPage, error) {
	var out TalkPage
	err := c.do(ctx, http.MethodGet, "/v1/orchestrators/"+url.PathEscape(worker)+"/stream"+q.Encode(), nil, &out)
	return out, err
}

// AssistantPolicies lists the versions of the assistant policy, newest first.
func (c *ProjectClient) AssistantPolicies(ctx context.Context) ([]authority.PolicyVersion, error) {
	var out PolicyList
	err := c.do(ctx, http.MethodGet, "/v1/policies?kind="+string(authority.PolicyAssistant), nil, &out)
	return out.Versions, err
}
