package authority

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"ropeshift.dev/ropeshift/internal/ledger"
)

// SubjectApproval names an approval as the subject of an event.
const SubjectApproval = "approval"

// EventApprovalChanged records an approval as it stands now, so a replay needs nothing else.
const EventApprovalChanged = "approval.changed"

// What an approval is about (FR-AUTH-2). The digest of each covers a different thing.
const (
	// KindToolCall covers a tool's name and its canonical input.
	KindToolCall = "tool_call"
	// KindArtifact covers an artifact's content or commit.
	KindArtifact = "artifact"
	// KindContract covers a contract's digest.
	KindContract = "contract"
)

// How an approval's outcome reached its session.
const (
	// ChannelHook is an answer a waiting hook returned.
	ChannelHook = "hook"
	// ChannelTerminal is a person at a pane, which a tool's end shows and nothing proves.
	ChannelTerminal = "terminal"
)

// ApprovalState is where a request stands.
type ApprovalState string

// The states of an approval. Only `approved` lets anything run, and only once.
const (
	ApprovalPending  ApprovalState = "pending"
	ApprovalApproved ApprovalState = "approved"
	ApprovalDenied   ApprovalState = "denied"
	// ApprovalUsed is an approved request that ran, which grants nothing more.
	ApprovalUsed ApprovalState = "used"
	// ApprovalVoid is one whose artifact changed. The request must be raised again.
	ApprovalVoid ApprovalState = "void"
	// ApprovalExpired is one that can no longer return to its session (FR-AUTH-7).
	ApprovalExpired ApprovalState = "expired"
	// ApprovalAnswered is one a person answered at a terminal, which no session authenticated, so
	// it records what happened and grants nothing.
	ApprovalAnswered ApprovalState = "answered"
)

// The ways a decision is refused.
var (
	// ErrSelfApproval means a principal decided its own request (FR-AUTH-1).
	ErrSelfApproval = errors.New("a principal cannot decide its own request")
	// ErrStaleDigest means a decider saw something other than what the request is bound to.
	ErrStaleDigest = errors.New("the digest does not match the request")
	// ErrApprovalClosed means a request has reached an end, so nothing can decide or use it again.
	ErrApprovalClosed = errors.New("this request is closed")
	// ErrNotDelegated means a request falls outside every delegated class, so it goes to a member.
	ErrNotDelegated = errors.New("this request is outside the delegation policy")
	// ErrDeciderKind means a kind of principal that decides nothing tried to.
	ErrDeciderKind = errors.New("this principal cannot decide a request")
	// ErrBadApproval means a request cannot be recorded as it stands.
	ErrBadApproval = errors.New("invalid approval request")
)

// Principal names a caller in `requested_by` and `decided_by`, and a store compares these two, so
// the kind is part of the name.
func Principal(actor ledger.Actor) string { return string(actor.Kind) + ":" + actor.ID }

// Approval is one request, bound to an exact artifact.
type Approval struct {
	ID         string `json:"id"`
	Lane       string `json:"lane_id"`
	WorkOrder  string `json:"work_order_id"`
	Generation int64  `json:"generation"`
	Kind       string `json:"kind"`
	// Subject is what is asked about, which a later request of the same lane can replace: a tool's
	// name, or an artifact's ID.
	Subject string `json:"subject"`
	// SubjectDigest is the exact artifact. For a tool call it covers Input and Path.
	SubjectDigest string `json:"subject_digest"`
	Input         string `json:"input,omitempty"`
	// Command and Path are what a delegation class reads. Command comes from Input, and Path is where
	// the call lands on disk, which a worker reads against the lane's worktree.
	Command string `json:"command,omitempty"`
	Path    string `json:"path,omitempty"`
	// Summary is what a person reads, and it is built from Input, never from what a lane wrote.
	Summary     string        `json:"summary"`
	RequestedBy string        `json:"requested_by"`
	State       ApprovalState `json:"state"`
	// DecidedBy is a principal, DecidedDigest what it saw, and PolicyVersion the delegation policy in
	// force. A terminal's answer has none of these.
	DecidedBy     string `json:"decided_by,omitempty"`
	DecidedKind   string `json:"decided_kind,omitempty"`
	DecidedDigest string `json:"decided_digest,omitempty"`
	DecidedAt     int64  `json:"decided_at,omitempty"`
	Reason        string `json:"reason,omitempty"`
	PolicyVersion int64  `json:"policy_version,omitempty"`
	RaisedAt      int64  `json:"raised_at,omitempty"`
	ExpiresAt     int64  `json:"expires_at,omitempty"`
	Channel       string `json:"channel,omitempty"`
	// UsedBy names the one call that ran on this approval, so a call whose answer was lost hears
	// the same answer, and no other call does.
	UsedBy string `json:"used_by,omitempty"`
}

// Decision is what a principal says of a request. Digest is what it saw.
type Decision struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	Allow  bool   `json:"allow"`
	Reason string `json:"reason,omitempty"`
}

// Open says a request still waits on a decision.
func (a Approval) Open() bool { return a.State == ApprovalPending }

// Live says a request can still lead to a tool running.
func (a Approval) Live() bool { return a.State == ApprovalPending || a.State == ApprovalApproved }

// Decided says what `approval_decided` asks: a request has an answer, whatever it is.
func (a Approval) Decided() bool { return a.State != ApprovalPending }

// Validate refuses a request that binds to nothing.
func (a Approval) Validate() error {
	switch {
	case a.ID == "" || a.Lane == "" || a.Subject == "":
		return fmt.Errorf("%w: it names no lane or subject", ErrBadApproval)
	case a.SubjectDigest == "":
		return fmt.Errorf("%w: it binds to no digest", ErrBadApproval)
	case a.RequestedBy == "":
		return fmt.Errorf("%w: it names no requester", ErrBadApproval)
	case a.Kind != KindToolCall && a.Kind != KindArtifact && a.Kind != KindContract:
		return fmt.Errorf("%w: kind %q", ErrBadApproval, a.Kind)
	case a.Kind == KindToolCall && a.SubjectDigest != LandingDigest(a.Subject, a.Input, a.Path):
		return fmt.Errorf("%w: its digest does not cover its tool call", ErrBadApproval)
	}
	return nil
}

// Decide applies a decision. Each check repeats in the store where it can, and none reads anything
// a lane wrote: the digest is the one a decider saw, and the principal is the one a session proved.
func (a Approval) Decide(decision Decision, actor ledger.Actor, policy, at int64) (Approval, error) {
	if actor.Kind != ledger.ActorMember && actor.Kind != ledger.ActorOrchestrator {
		return a, fmt.Errorf("%w: %s", ErrDeciderKind, actor.Kind)
	}
	principal := Principal(actor)
	if principal == a.RequestedBy {
		return a, fmt.Errorf("%w: %s", ErrSelfApproval, principal)
	}
	if !a.Open() {
		return a, fmt.Errorf("%w: it is %s", ErrApprovalClosed, a.State)
	}
	if decision.Digest != a.SubjectDigest {
		return a, fmt.Errorf("%w: it is bound to %s", ErrStaleDigest, a.SubjectDigest)
	}
	a.State = ApprovalDenied
	if decision.Allow {
		a.State = ApprovalApproved
	}
	a.DecidedBy, a.DecidedKind, a.DecidedDigest = principal, string(actor.Kind), decision.Digest
	a.DecidedAt, a.Reason, a.PolicyVersion, a.Channel = at, decision.Reason, policy, ChannelHook
	return a, nil
}

// Repeats says a decision is one this request already took, which a retry after a lost answer is.
func (a Approval) Repeats(decision Decision, actor ledger.Actor) bool {
	allowed := a.State == ApprovalApproved || a.State == ApprovalUsed
	return a.DecidedBy == Principal(actor) && a.DecidedDigest == decision.Digest && allowed == decision.Allow &&
		(allowed || a.State == ApprovalDenied)
}

// Use spends an approval on one call. A digest that is not the one approved voids it, so what a
// reviewer saw is what runs, and a call that is not the one it was spent on is refused.
func (a Approval) Use(digest, call string) (Approval, error) {
	if a.State == ApprovalUsed && a.UsedBy == call && call != "" && digest == a.DecidedDigest {
		return a, nil
	}
	if a.State != ApprovalApproved {
		return a, fmt.Errorf("%w: it is %s", ErrApprovalClosed, a.State)
	}
	if digest != a.DecidedDigest || digest != a.SubjectDigest {
		a.State = ApprovalVoid
		return a, fmt.Errorf("%w: approved %s, and this is %s", ErrStaleDigest, a.DecidedDigest, digest)
	}
	a.State, a.UsedBy = ApprovalUsed, call
	return a, nil
}

// Replaced says a call with another digest leaves this request behind, for a lane now in generation.
// An approved request stands for one call, so a call of that tool with any other input is a use that
// fails its comparison. A request still open in this generation may be a second call of one turn, and
// one from an earlier generation has no session left to wait in.
func (a Approval) Replaced(generation int64) bool {
	return a.State == ApprovalApproved || (a.State == ApprovalPending && a.Generation < generation)
}

// maxRepeated bounds the input a prompt states whole. Past it a session hears the summary, and a call
// it cannot make again exactly asks again.
const maxRepeated = 1 << 16

// Repeat names the one call a decision covers, to a session that has to make it again. It states the
// whole input and not the summary, because the digest covers every field, and a session that
// remembers nothing of the call would write its own description.
func (a Approval) Repeat() string {
	if a.State != ApprovalApproved {
		return ""
	}
	if a.Input == "" || len(a.Input) > maxRepeated {
		return a.Summary
	}
	return "Tool: " + a.Subject + "\nInput: " + a.Input
}

// Outcome says how a request came out, in words the session that made it reads when it comes back.
// A request that is still open or that ran says nothing, and one that was approved is a call to
// make, which Repeat states.
func (a Approval) Outcome() string {
	switch a.State {
	case ApprovalDenied:
		outcome := "A request this lane made was denied, so do not make this call again: " + a.Summary
		if a.Reason != "" {
			outcome += ". The reason given: " + a.Reason
		}
		return outcome
	case ApprovalVoid, ApprovalExpired:
		return "A request this lane made closed with no decision, so nothing allows this call. Make it again to ask again: " + a.Summary
	}
	return ""
}

// ApprovalUse is one look at a lane's call, from the worker that holds the lane. Peek reads what a
// hook needs before a turn ends, and spends nothing.
type ApprovalUse struct {
	Auth       ledger.WorkerAuth `json:"-"`
	Epoch      int64             `json:"epoch"`
	WorkOrder  string            `json:"work_order"`
	Fence      int64             `json:"fence"`
	Lane       string            `json:"lane"`
	Generation int64             `json:"generation"`
	Subject    string            `json:"subject"`
	Digest     string            `json:"digest"`
	Call       string            `json:"call"`
	Peek       bool              `json:"peek,omitempty"`
}

// ApprovalAnswer is where one call stands. Empty says no request stands for it, so a worker raises
// one, or nothing it raised has arrived yet.
type ApprovalAnswer struct {
	ID     string        `json:"id,omitempty"`
	State  ApprovalState `json:"state,omitempty"`
	Reason string        `json:"reason,omitempty"`
	// Allowed says this call may run, which only a decision spent on it can say.
	Allowed bool `json:"allowed,omitempty"`
}

// WorkerDecision is what a worker's orchestrator decided.
type WorkerDecision struct {
	Auth     ledger.WorkerAuth `json:"-"`
	Epoch    int64             `json:"epoch"`
	Decision Decision          `json:"decision"`
}

// TerminalAnswer is what a session's hooks showed of a call with an open request: its tool ended, or
// its turn did with no tool run. Note says which, because neither proves who answered.
type TerminalAnswer struct {
	Digest string `json:"digest"`
	Note   string `json:"note,omitempty"`
}

// AnswerOf reads one request for one call. Only a decision spent on this very call allows it.
func AnswerOf(a Approval, use ApprovalUse) ApprovalAnswer {
	answer := ApprovalAnswer{ID: a.ID, State: a.State, Reason: a.Reason}
	answer.Allowed = a.State == ApprovalUsed && a.UsedBy == use.Call && use.Call != ""
	return answer
}

// Standing reads a lane's requests for one call, and says whether anything needs a write: a decision
// to spend, or what a changed call left behind.
func Standing(rows []Approval, use ApprovalUse) (ApprovalAnswer, bool) {
	var answer ApprovalAnswer
	for _, row := range rows {
		if row.Subject != use.Subject {
			continue
		}
		if row.SubjectDigest != use.Digest {
			if row.Replaced(use.Generation) {
				return ApprovalAnswer{}, true
			}
			continue
		}
		if row.State == ApprovalApproved && !use.Peek {
			return ApprovalAnswer{}, true
		}
		answer = AnswerOf(row, use)
	}
	return answer, false
}

// CanonicalInput reads a tool's input the one way a digest covers. JSON has many spellings of one
// object, and a number must keep every digit, so the bytes a digest reads come from this.
func CanonicalInput(raw []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadApproval, err)
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadApproval, err)
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

// ToolDigest covers a tool's name and its canonical input, which is all a hook can read of a call.
func ToolDigest(tool, canonical string) string {
	sum := sha256.Sum256([]byte(tool + "\x00" + canonical))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// LandingDigest is what a decision binds to: a call's input, and the path it lands on. A path is a
// fact about the disk and not about the input, so one input can land somewhere else later, and a
// digest of the input alone would call that the same call. JSON escapes a zero byte, so the one here
// cannot be spelled in an input. A call with no path binds to its input.
func LandingDigest(tool, canonical, landing string) string {
	if landing == "" {
		return ToolDigest(tool, canonical)
	}
	sum := sha256.Sum256([]byte(tool + "\x00" + canonical + "\x00" + landing))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ToolCall is what a request says of a call.
type ToolCall struct {
	Tool  string
	Input string
	// Digest is what a decision binds to, and it covers the input alone until Lands reads where the
	// call goes. Seen always does, because a hook knows no worktree: it is how a hook names this call.
	Digest  string
	Seen    string
	Command string
	Path    string
	Summary string
}

// Lands binds a call to where it lands, which only a worker can read. A reviewer reads that too when
// it is not what the input says, because a call that names one path and lands on another must not
// read as the first.
func (c ToolCall) Lands(landing string) ToolCall {
	if landing != c.Path {
		c.Summary += " (this lands on " + landing + ")"
	}
	c.Path, c.Digest = landing, LandingDigest(c.Tool, c.Input, landing)
	return c
}

// maxSummary bounds what a person reads of one call. A longer call says so, because a person must
// not take the part they saw for the whole of it.
const maxSummary = 4096

// ReadToolCall reads a call's input. A tool's fields have no shape this code owns, so anything but
// a command or a path reads as the whole of the input. It reads each by its exact key, because a
// decoder that folds a key's case would take a field the tool never reads for the one it does, and
// a call that names two paths names none a class can read.
func ReadToolCall(tool string, raw []byte) (ToolCall, error) {
	canonical, err := CanonicalInput(raw)
	if err != nil {
		return ToolCall{}, err
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(canonical), &fields); err != nil {
		fields = nil
	}
	field := func(key string) string {
		value, _ := fields[key].(string)
		return value
	}
	digest := ToolDigest(tool, canonical)
	call := ToolCall{Tool: tool, Input: canonical, Digest: digest, Seen: digest, Command: field("command"), Path: field("file_path")}
	if notebook := field("notebook_path"); call.Path == "" {
		call.Path = notebook
	} else if notebook != "" && notebook != call.Path {
		call.Path = ""
	}
	text := canonical
	if call.Command != "" {
		text = call.Command
	}
	if len(text) > maxSummary {
		text = fmt.Sprintf("%s ... (%d more bytes, which the digest covers)", text[:maxSummary], len(text)-maxSummary)
	}
	call.Summary = tool + ": " + text
	return call, nil
}

// ReplayApprovals rebuilds every request from the log, whose events each hold a whole record.
func ReplayApprovals(events []ledger.Event) (map[string]Approval, error) {
	book := map[string]Approval{}
	for _, e := range events {
		if e.Type != EventApprovalChanged {
			continue
		}
		var approval Approval
		if err := json.Unmarshal(e.Data, &approval); err != nil {
			return nil, fmt.Errorf("approval replay: rev %d: %w", e.Rev, err)
		}
		book[approval.ID] = approval
	}
	return book, nil
}

// ApprovalEvent is the event of a request as it stands now.
func ApprovalEvent(approval Approval, actor ledger.Actor) (ledger.EventDraft, error) {
	data, err := json.Marshal(approval)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{
		Type: EventApprovalChanged, Actor: actor, Subject: ledger.Subject{Kind: SubjectApproval, ID: approval.ID}, Data: data,
	}, nil
}

// PolicyDelegation governs what an orchestrator may decide (FR-AUTH-6).
const PolicyDelegation PolicyKind = "delegation"

// DelegationClass names one kind of request an orchestrator may decide: a tool, and either a
// command prefix or a path. A class with neither would cover a tool outright, so none may.
type DelegationClass struct {
	Tool          string `toml:"tool" json:"tool"`
	CommandPrefix string `toml:"command_prefix,omitempty" json:"command_prefix,omitempty"`
	Path          string `toml:"path,omitempty" json:"path,omitempty"`
}

// shellMeta holds what makes a command more than one command, or more than what it reads as.
const shellMeta = ";&|<>$`\\\n\r(){}"

// covers says a class reaches a call. A prefix reaches one simple command, and any shell syntax
// falls to a member, because `go test; rm -rf .` starts with `go test`. A class reads one field, so
// a call that also carries the other is more than the class declared.
func (c DelegationClass) covers(a Approval) bool {
	if a.Kind != KindToolCall || a.Subject != c.Tool {
		return false
	}
	if c.CommandPrefix != "" {
		command := a.Command
		if a.Path != "" || strings.ContainsAny(command, shellMeta) {
			return false
		}
		return command == c.CommandPrefix || strings.HasPrefix(command, c.CommandPrefix+" ")
	}
	return c.Path != "" && a.Command == "" && pathCovered(c.Path, a.Path)
}

// pathCovered reads a worktree-relative path against a class's pattern, where a trailing `/**`
// takes everything under a directory.
func pathCovered(pattern, target string) bool {
	if target == "" || path.IsAbs(target) || target != path.Clean(target) || target == ".." || strings.HasPrefix(target, "../") {
		return false
	}
	if dir, ok := strings.CutSuffix(pattern, "/**"); ok {
		return strings.HasPrefix(target, dir+"/")
	}
	matched, err := path.Match(pattern, target)
	return err == nil && matched
}

// DelegationPolicy is the body of a delegation policy version.
type DelegationPolicy struct {
	Version int64             `toml:"version" json:"version"`
	Classes []DelegationClass `toml:"classes" json:"classes"`
}

// DelegationPolicyFile is the table a member publishes.
type DelegationPolicyFile struct {
	DelegationPolicy DelegationPolicy `toml:"delegation_policy"`
}

// DelegationDraft is a delegation policy as written, and what it says.
type DelegationDraft struct {
	Body   string
	Policy DelegationPolicy
}

// BuiltInDelegationBody is what a workspace runs on until a member publishes one, which delegates
// nothing: an orchestrator decides only what a person declared.
const BuiltInDelegationBody = `[delegation_policy]
version = 1
classes = []
`

// Check accepts a policy a workspace can run on.
func (p DelegationPolicy) Check() error {
	if p.Version < 1 {
		return fmt.Errorf("%w: version %d", ErrBadLanePolicy, p.Version)
	}
	for _, class := range p.Classes {
		if class.Tool == "" || (class.CommandPrefix == "") == (class.Path == "") {
			return fmt.Errorf("%w: a class names a tool and exactly one of a command prefix or a path: %+v", ErrBadLanePolicy, class)
		}
		if strings.ContainsAny(class.CommandPrefix, shellMeta) {
			return fmt.Errorf("%w: a prefix holds shell syntax: %q", ErrBadLanePolicy, class.CommandPrefix)
		}
	}
	return nil
}

// Covers says an orchestrator may decide a request.
func (p DelegationPolicy) Covers(a Approval) bool {
	for _, class := range p.Classes {
		if class.covers(a) {
			return true
		}
	}
	return false
}

// CheckDelegated holds an orchestrator to the policy in force, and a member to none. A body that no
// longer matches its digest stops the decision, because a member reviewed other text.
func CheckDelegated(a Approval, version PolicyVersion, actor ledger.Actor) error {
	if actor.Kind != ledger.ActorOrchestrator {
		return nil
	}
	policy, err := ReadDelegation(version)
	if err != nil {
		return err
	}
	if !policy.Covers(a) {
		return fmt.Errorf("%w: %s goes to a member", ErrNotDelegated, a.ID)
	}
	return nil
}

// NewDelegationVersion stores a draft under a generated ID.
func NewDelegationVersion(id, authoredBy string, draft DelegationDraft, createdAt int64) (PolicyVersion, error) {
	settings, err := json.Marshal(draft.Policy)
	if err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		ID: id, Kind: PolicyDelegation, Version: draft.Policy.Version, Digest: PolicyDigest(draft.Body), Body: draft.Body,
		Settings: string(settings), AuthoredBy: authoredBy, CreatedAt: createdAt,
	}, nil
}

// ReadDelegation reads a version's settings, once its body is what its digest covers.
func ReadDelegation(version PolicyVersion) (DelegationPolicy, error) {
	if err := version.CheckDigest(); err != nil {
		return DelegationPolicy{}, err
	}
	var policy DelegationPolicy
	if err := json.Unmarshal([]byte(version.Settings), &policy); err != nil {
		return DelegationPolicy{}, err
	}
	return policy, nil
}
