package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// The events about a plan approval that record the creation of a repository its version names.
const (
	// EventRepositoryCreationStarted records a creation about to write anything: a CreationStartedEvent.
	EventRepositoryCreationStarted = "approval.repository_creation_started"
	// EventRepositoryCreationFinished records what a creation came to: a CreationFinishedEvent.
	EventRepositoryCreationFinished = "approval.repository_creation_finished"
	// EventRepositoryCreationSkipped records a member leaving a creation undone: a CreationSkippedEvent.
	EventRepositoryCreationSkipped = "approval.repository_creation_skipped"
)

// CreationState is where the events about one entry's creation leave it.
type CreationState string

// The states of a creation. A finished creation is in the state of its outcome.
const (
	// CreationUntried is an entry no creation event names.
	CreationUntried CreationState = ""
	// CreationStarted is a creation that started and has no later event: it runs now, or it was interrupted.
	CreationStarted CreationState = "started"
	// CreationCreated is a creation every step of which is done.
	CreationCreated CreationState = "created"
	// CreationRefused is a creation a check refused, with nothing written.
	CreationRefused CreationState = "refused"
	// CreationFailed is a creation that stopped at a step that failed, keeping what it made.
	CreationFailed CreationState = "failed"
	// CreationSkipped is a creation a member chose to leave undone.
	CreationSkipped CreationState = "skipped"
)

// CreationStartedEvent is the data of EventRepositoryCreationStarted.
type CreationStartedEvent struct {
	Name         string `json:"name"`
	Key          string `json:"key"`
	ManifestPath string `json:"manifest_path"`
	// Path is where the entry's path leads on the worker's machine.
	Path          string `json:"path"`
	Forge         string `json:"forge"`
	Owner         string `json:"owner"`
	ForgeName     string `json:"forge_name"`
	Visibility    string `json:"visibility"`
	DefaultBranch string `json:"default_branch,omitempty"`
	// Worker is the worker whose machine runs the creation.
	Worker string `json:"worker,omitempty"`
}

// CreationFinishedEvent is the data of EventRepositoryCreationFinished: the outcome, which is CreationCreated,
// CreationRefused, or CreationFailed, the reason when it is not created, and the creation's own fields.
type CreationFinishedEvent struct {
	Name    string        `json:"name"`
	Key     string        `json:"key"`
	Outcome CreationState `json:"outcome"`
	Reason  string        `json:"reason,omitempty"`
	CreationOutcome
}

// CreationSkippedEvent is the data of EventRepositoryCreationSkipped.
type CreationSkippedEvent struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// CreationRecord is the latest of the events about one entry's creation.
type CreationRecord struct {
	State CreationState
	// Reason is why the creation was refused, failed, or skipped.
	Reason string
	// Started is the creation's latest start, which a later finished event clears, and Finished is the latest finish,
	// which a later start clears.
	Started  *CreationStartedEvent
	Finished *CreationFinishedEvent
}

// LatestCreation reads where the events about one plan approval, in the order they were recorded, leave the creation
// of the entry named. Events of other types and of other entries are not read.
func LatestCreation(events []Event, name string) (CreationRecord, error) {
	var out CreationRecord
	for _, event := range events {
		switch event.Type {
		case EventRepositoryCreationStarted:
			var data CreationStartedEvent
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return CreationRecord{}, fmt.Errorf("event %d: %w", event.Rev, err)
			}
			if data.Name == name {
				out = CreationRecord{State: CreationStarted, Started: &data}
			}
		case EventRepositoryCreationFinished:
			var data CreationFinishedEvent
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return CreationRecord{}, fmt.Errorf("event %d: %w", event.Rev, err)
			}
			if data.Name == name {
				out = CreationRecord{State: data.Outcome, Reason: data.Reason, Finished: &data}
			}
		case EventRepositoryCreationSkipped:
			var data CreationSkippedEvent
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return CreationRecord{}, fmt.Errorf("event %d: %w", event.Rev, err)
			}
			if data.Name == name {
				out = CreationRecord{State: CreationSkipped, Reason: data.Reason}
			}
		}
	}
	return out, nil
}

// RepositoryCreation is one repository a plan approval creates on the worker's machine.
type RepositoryCreation struct {
	// Key is the creation's journal name, from the approval and the entry name.
	Key string `json:"key"`
	// Project is the project's name, rendered and used in the first commit's message.
	Project string `json:"project"`
	// Name is the manifest's name for the repository.
	Name string `json:"name"`
	// Path is absolute and clean: the planning clone's root joined with the manifest path.
	Path string `json:"path"`
	// Bound is the resolved directory that holds the planning repository's clone.
	Bound string `json:"bound"`
	// PlanningClone is the resolved planning clone, which the path may not be.
	PlanningClone string `json:"planning_clone"`
	// Create is the forge, owner, and visibility of the repository to create.
	Create        RemoteCreate `json:"create"`
	ForgeName     string       `json:"forge_name"`
	DefaultBranch string       `json:"default_branch,omitempty"`
	// Wait says the check waits, until its context ends, for another creation or check on the worker that holds the
	// folder the path leads to, as a real run's check does. A preview's check leaves it false and is refused at once.
	Wait bool `json:"wait,omitempty"`
}

// CreationCheck is what the checks that come before a creation's first write found.
type CreationCheck struct {
	// Refusal is the report of the check that failed, or nil when every check passed.
	Refusal *TemplateWriteReport
	// Leads is where the path leads, every link followed: the folder the creation writes, which the preview shows.
	// It is empty only when the check refused before it read the path.
	Leads string
	// URL is the origin the creation adds, as gh's git protocol names it.
	URL   string
	Login string
	// Branch is the branch the first commit starts on.
	Branch string
}

// CreationOutcome is what a creation did, as a plan approval records it.
type CreationOutcome struct {
	Forge         string       `json:"forge"`
	Owner         string       `json:"owner"`
	ForgeName     string       `json:"forge_name"`
	Visibility    string       `json:"visibility"`
	DefaultBranch string       `json:"default_branch,omitempty"`
	URL           string       `json:"url,omitempty"`
	Login         string       `json:"login,omitempty"`
	Commit        string       `json:"commit,omitempty"`
	CommitError   string       `json:"commit_error,omitempty"`
	Steps         []RemoteStep `json:"steps,omitempty"`
	Remaining     []string     `json:"remaining,omitempty"`
	Remedy        string       `json:"remedy,omitempty"`
	// Makefile is the REPOSITORIES entry to add to the workspace Makefile, name=<url>=<manifest path>, set once the
	// repository is created.
	Makefile string `json:"makefile,omitempty"`
}

// CreationKey is the name of a creation's journal: the SHA-256 of the approval's ID and the entry's name, in lowercase
// hex. The same approval and name give the same key, and another of either gives another.
func CreationKey(approval, name string) string {
	sum := sha256.Sum256([]byte(approval + "\x00" + name))
	return hex.EncodeToString(sum[:])
}
