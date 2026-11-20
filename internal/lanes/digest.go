package lanes

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
)

// DigestSource says where a digest's facts come from: the stream of a headless session, or the hooks of an
// interactive one.
type DigestSource string

// The sources of a digest.
const (
	DigestStream DigestSource = "stream"
	DigestHooks  DigestSource = "hooks"
)

// MaxDigestFiles is how many written paths a digest keeps. Writes to other paths are counted.
const MaxDigestFiles = 200

// The hooks a digest reads.
const (
	EventUserPromptSubmit = "UserPromptSubmit"
	EventPreToolUse       = "PreToolUse"
	EventStop             = "Stop"
)

// ToolUse is one call a session made: the tool's name, and the path a file-writing tool was called with. It is
// all a digest reads of a call, so no other input of a call leaves the session.
type ToolUse struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// WrittenPath is the path a file-writing tool was called with, given the `file_path` and `notebook_path` of
// the call's input, and empty for any other tool.
func WrittenPath(tool, filePath, notebookPath string) string {
	switch tool {
	case "Write", "Edit", "MultiEdit":
		return filePath
	case "NotebookEdit":
		return notebookPath
	}
	return ""
}

// Digest is what one generation of a lane, an orchestrator, or an assistant did, built from events the worker
// already receives (FR-SLOG-1). It holds no tool input but the written paths, and no prompt or response text.
// A field its source cannot give is absent and never zero: no API time comes from hooks, and no turn time
// from a stream that reported none.
type Digest struct {
	Source DigestSource `json:"source"`
	Turns  int64        `json:"turns"`
	// ToolCalls counts the calls of each tool by name, a subagent's included.
	ToolCalls map[string]int64 `json:"tool_calls"`
	// FilesWritten are the paths the file-writing tools were called with, relative to the worktree where they
	// lie in it, and FilesWrittenMore counts the writes to paths past the first MaxDigestFiles.
	FilesWritten     []string `json:"files_written"`
	FilesWrittenMore int64    `json:"files_written_more,omitempty"`
	// StartedAt and EndedAt are epoch milliseconds, and EndedAt is absent until the generation ends.
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at,omitempty"`
	TurnMs    *int64 `json:"turn_ms,omitempty"`
	APIMs     *int64 `json:"api_ms,omitempty"`
	// Complete is false when the worker knows it missed events.
	Complete bool `json:"complete"`
}

// DigestState is a digest and what a worker needs to go on building it after a restart.
type DigestState struct {
	Digest Digest `json:"digest"`
	// Root is the worktree, which written paths are made relative to.
	Root string `json:"root,omitempty"`
	// Through is the last stream position folded, and a position at or before it is read again and changes
	// nothing.
	Through int64 `json:"through,omitempty"`
	// LastAPI is the API time the stream last totalled, which the next total is a difference from.
	LastAPI *int64 `json:"last_api_ms,omitempty"`
	// Open says a turn has begun and no `Stop` has ended it, and OpenMs is when it began.
	Open   bool  `json:"open,omitempty"`
	OpenMs int64 `json:"open_ms,omitempty"`
	// Before is what the worktree held changed or untracked when the session began its first turn, and nil until
	// a worker read it (FR-LANE-20). It stays with the worker and never reaches the ledger.
	Before *[]string `json:"before,omitempty"`
}

// DigestRecord is what a worker's journal keeps of one generation's digest: the state a builder goes on from,
// whether the generation is over so that the state is final, and whether the event that carries the digest to
// the ledger is journaled.
type DigestRecord struct {
	Lane       string
	Generation int64
	State      []byte
	Over       bool
	Recorded   bool
}

// Digest is the digest the record's state holds.
func (r DigestRecord) Digest() (Digest, error) {
	builder, err := RestoreDigest(r.State)
	if err != nil {
		return Digest{}, err
	}
	return builder.Digest(), nil
}

// TurnFacts is what a stream's `result` says of a turn. A figure the line did not carry is nil.
type TurnFacts struct {
	DurationMs *int64
	// APIMs is the running total of API time the stream reports, and not the turn's own.
	APIMs *int64
	// Compaction says the turn held only a compaction, which is no turn of the session's work.
	Compaction bool
}

// DigestBuilder folds a generation's events into its digest. It reads no clock, file, or process: a caller
// says when things happened.
type DigestBuilder struct {
	state DigestState
}

// NewDigest begins a digest for a generation that started at a time, where root is the worktree.
func NewDigest(source DigestSource, root string, startedAt int64) *DigestBuilder {
	return &DigestBuilder{state: DigestState{
		Root: strings.TrimSuffix(root, "/"),
		Digest: Digest{
			Source: source, ToolCalls: map[string]int64{}, FilesWritten: []string{}, StartedAt: startedAt, Complete: true,
		},
	}}
}

// RestoreDigest continues a digest from the state Marshal gave.
func RestoreDigest(raw []byte) (*DigestBuilder, error) {
	var state DigestState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("a digest state: %w", err)
	}
	if state.Digest.Source != DigestStream && state.Digest.Source != DigestHooks {
		return nil, fmt.Errorf("a digest state names the source %q", state.Digest.Source)
	}
	if state.Digest.ToolCalls == nil {
		state.Digest.ToolCalls = map[string]int64{}
	}
	if state.Digest.FilesWritten == nil {
		state.Digest.FilesWritten = []string{}
	}
	return &DigestBuilder{state: state}, nil
}

// Marshal is the state a worker keeps while a generation runs.
func (b *DigestBuilder) Marshal() ([]byte, error) {
	return json.Marshal(b.state)
}

// Source says which events this digest folds.
func (b *DigestBuilder) Source() DigestSource { return b.state.Digest.Source }

// Missed records that the worker knows it missed events of this generation.
func (b *DigestBuilder) Missed() { b.state.Digest.Complete = false }

// Begin keeps what the worktree held before the session's first turn, and says whether it kept it: the first
// reading is the one that stands.
func (b *DigestBuilder) Begin(changes []string) bool {
	if b.state.Before != nil {
		return false
	}
	before := slices.Clone(changes)
	if before == nil {
		before = []string{}
	}
	b.state.Before = &before
	return true
}

// Before is what Begin kept, and whether it kept anything.
func (b *DigestBuilder) Before() ([]string, bool) {
	if b.state.Before == nil {
		return nil, false
	}
	return slices.Clone(*b.state.Before), true
}

// stale says a stream position was folded already. Position zero is no position, so it is never stale.
func (b *DigestBuilder) stale(seq int64) bool {
	if seq == 0 {
		return false
	}
	if seq <= b.state.Through {
		return true
	}
	b.state.Through = seq
	return false
}

// Calls folds the tool calls of one stream message, at its position in the stream.
func (b *DigestBuilder) Calls(seq int64, uses []ToolUse) {
	if b.stale(seq) {
		return
	}
	for _, use := range uses {
		b.call(use.Name, use.Path)
	}
}

func (b *DigestBuilder) call(tool, written string) {
	if tool == "" {
		return
	}
	b.state.Digest.ToolCalls[tool]++
	if written == "" {
		return
	}
	written = b.relative(written)
	switch {
	case slices.Contains(b.state.Digest.FilesWritten, written):
	case len(b.state.Digest.FilesWritten) < MaxDigestFiles:
		b.state.Digest.FilesWritten = append(b.state.Digest.FilesWritten, written)
	default:
		b.state.Digest.FilesWrittenMore++
	}
}

// relative makes a path relative to the worktree where it lies in it. A path outside it stays as it was named.
func (b *DigestBuilder) relative(written string) string {
	written = path.Clean(written)
	if b.state.Root == "" || !path.IsAbs(written) {
		return written
	}
	if rest, inside := strings.CutPrefix(written, b.state.Root+"/"); inside {
		return rest
	}
	return written
}

// Turn folds one stream `result`, at its position in the stream: a turn, its duration, and the API time since
// the last total. A total that fell is a session that started over, and counts whole.
func (b *DigestBuilder) Turn(seq int64, turn TurnFacts) {
	if b.stale(seq) || turn.Compaction {
		return
	}
	digest := &b.state.Digest
	digest.Turns++
	if turn.DurationMs != nil {
		digest.TurnMs = add(digest.TurnMs, *turn.DurationMs)
	}
	if turn.APIMs == nil {
		return
	}
	since := *turn.APIMs
	if last := b.state.LastAPI; last != nil && *turn.APIMs >= *last {
		since = *turn.APIMs - *last
	}
	total := *turn.APIMs
	b.state.LastAPI = &total
	digest.APIMs = add(digest.APIMs, since)
}

func add(sum *int64, more int64) *int64 {
	if sum != nil {
		more += *sum
	}
	return &more
}

// Hook folds one hook of an interactive session. A turn begins at its prompt and ends at its `Stop`, and a
// `Stop` with no prompt before it is a turn whose beginning the worker did not see.
func (b *DigestBuilder) Hook(event HookEvent) {
	digest := &b.state.Digest
	if event.Malformed {
		digest.Complete = false
		return
	}
	switch event.Event {
	case EventUserPromptSubmit:
		digest.Turns++
		b.state.Open, b.state.OpenMs = true, event.AtMs
	case EventPreToolUse:
		written := event.Path
		if event.Detail == DetailDenied {
			written = ""
		}
		b.call(event.Tool, written)
	case EventStop:
		if !b.state.Open {
			digest.Turns++
			digest.Complete = false
			return
		}
		if began := b.state.OpenMs; began > 0 && event.AtMs >= began {
			digest.TurnMs = add(digest.TurnMs, event.AtMs-began)
		}
		b.state.Open = false
	}
}

// Finish says when the generation ended, and returns the digest. It can be called again.
func (b *DigestBuilder) Finish(endedAt int64) Digest {
	b.state.Digest.EndedAt = endedAt
	return b.Digest()
}

// Digest is a copy of the digest so far.
func (b *DigestBuilder) Digest() Digest {
	out := b.state.Digest
	out.ToolCalls = make(map[string]int64, len(out.ToolCalls))
	for name, count := range b.state.Digest.ToolCalls {
		out.ToolCalls[name] = count
	}
	out.FilesWritten = slices.Clone(b.state.Digest.FilesWritten)
	return out
}
