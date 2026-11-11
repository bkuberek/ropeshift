package lanes

import (
	"fmt"
	"strconv"
	"strings"
)

// The resource attributes every lane's telemetry carries, so an agent's own data points join the
// work order, lane, and generation that produced them (FR-OBS-2).
const (
	// AttrProject is the lane's project (ADR-0021), and AttrWorkspace holds the same ID, which every
	// record of the project carries.
	AttrProject    = "ropeshift.project"
	AttrWorkspace  = "ropeshift.workspace"
	AttrWorkOrder  = "ropeshift.work_order.id"
	AttrLane       = "ropeshift.lane.id"
	AttrRole       = "ropeshift.lane.role"
	AttrGeneration = "ropeshift.generation"
	AttrProfile    = "ropeshift.profile"
	AttrRepo       = "ropeshift.repo"
	AttrBranch     = "ropeshift.branch"
	AttrWorker     = "ropeshift.worker"
	AttrDriver     = "ropeshift.driver"
)

// TelemetrySettings is a profile's `[telemetry]` table. The first three are a lane's, and the rest
// are the worker's, which only the user's layer sets.
type TelemetrySettings struct {
	// Traces turns on the agent's traces, which are beta.
	Traces bool `toml:"traces" json:"traces"`
	// Content lets prompts, responses, and tool content into the agent's telemetry. It is off unless a
	// person turns it on, and only the user's layer can (FR-OBS-5).
	Content bool `toml:"content" json:"content"`
	// OmitRepository leaves the repository and branch out of a lane's attributes.
	OmitRepository bool `toml:"omit_repository" json:"omit_repository"`
	// RetentionAge and RetentionBytes bound the worker's local store, whichever comes first (D3).
	RetentionAge   string `toml:"retention_age" json:"retention_age"`
	RetentionBytes int64  `toml:"retention_bytes" json:"retention_bytes"`
	// ExportAccount keeps account identifiers on export. They stay in the local store either way.
	ExportAccount bool `toml:"export_account_attributes" json:"export_account_attributes"`
}

// WorkerSettings is the part of the table the worker reads, which is one per machine.
func (s TelemetrySettings) WorkerSettings() TelemetrySettings {
	return TelemetrySettings{RetentionAge: s.RetentionAge, RetentionBytes: s.RetentionBytes, ExportAccount: s.ExportAccount}
}

// TelemetryTags is what identifies one generation's telemetry.
type TelemetryTags struct {
	Project    string
	Workspace  string
	WorkOrder  string
	Lane       string
	Role       string
	Generation int64
	Profile    string
	Repo       string
	Branch     string
	Worker     string
	Driver     string
}

// Omitting drops what a profile keeps out. The repository and branch are not content, and a
// profile can still leave them out.
func (t TelemetryTags) Omitting(settings TelemetrySettings) TelemetryTags {
	if settings.OmitRepository {
		t.Repo, t.Branch = "", ""
	}
	return t
}

// Pairs lists the tags in a fixed order, leaving out what is empty.
func (t TelemetryTags) Pairs() [][2]string {
	generation := ""
	if t.Generation > 0 {
		generation = strconv.FormatInt(t.Generation, 10)
	}
	all := [][2]string{
		{AttrProject, t.Project}, {AttrWorkspace, t.Workspace}, {AttrWorkOrder, t.WorkOrder}, {AttrLane, t.Lane}, {AttrRole, t.Role},
		{AttrGeneration, generation}, {AttrProfile, t.Profile}, {AttrRepo, t.Repo}, {AttrBranch, t.Branch},
		{AttrWorker, t.Worker}, {AttrDriver, t.Driver},
	}
	out := make([][2]string, 0, len(all))
	for _, pair := range all {
		if pair[1] != "" {
			out = append(out, pair)
		}
	}
	return out
}

// ResourceAttributes renders the tags as `OTEL_RESOURCE_ATTRIBUTES`: comma-separated pairs whose
// values hold no space, comma, or other character the variable's grammar refuses, because each
// such byte is percent-encoded.
func (t TelemetryTags) ResourceAttributes() string {
	pairs := t.Pairs()
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, pair[0]+"="+encodeAttribute(pair[1]))
	}
	return strings.Join(parts, ",")
}

// encodeAttribute keeps printable US-ASCII and percent-encodes everything else, along with the
// characters the grammar gives a meaning: a comma, a semicolon, a quote, a backslash, an equals
// sign, and the percent sign itself.
func encodeAttribute(value string) string {
	var b strings.Builder
	for _, c := range []byte(value) {
		if c > 0x20 && c < 0x7f && !strings.ContainsRune(`,;"\=%`, rune(c)) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}
