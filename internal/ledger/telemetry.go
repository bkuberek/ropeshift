package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// The names of ropeshift's own spans (observability.md). Metric and attribute names stay proposals
// until the conventions settle.
const (
	SpanWorkOrder     = "ropeshift.work_order"
	SpanClaim         = "ropeshift.claim"
	SpanLane          = "ropeshift.lane"
	SpanGeneration    = "ropeshift.generation"
	SpanEffect        = "ropeshift.effect"
	SpanHandoff       = "ropeshift.handoff"
	SpanPark          = "ropeshift.park"
	SpanApprovalWait  = "ropeshift.approval.wait"
	SpanQuestion      = "ropeshift.question.ladder"
	SpanMessageSend   = "ropeshift.message.send"
	SpanMessageInject = "ropeshift.message.inject"
)

// The names of ropeshift's own metrics.
const (
	MetricContextTokens   = "ropeshift.lane.context.tokens"
	MetricContextBaseline = "ropeshift.lane.context.baseline"
	MetricTokens          = "ropeshift.lane.tokens"
	MetricCost            = "ropeshift.lane.cost"
	// MetricAgentTokens counts the tokens a roster agent spent inside a lane, where the driver reports
	// which agent spent them (FR-AGENT-12). They are part of the lane's own usage, kept apart so a lane's
	// tokens break down by agent without counting twice.
	MetricAgentTokens     = "ropeshift.lane.agent.tokens"
	MetricStall           = "ropeshift.lane.stall.duration"
	MetricRespawns        = "ropeshift.lane.respawns"
	MetricBreakerTrips    = "ropeshift.lane.breaker.trips"
	MetricLivenessUnknown = "ropeshift.lane.liveness.unknown"
	MetricApprovalWait    = "ropeshift.approval.wait.duration"
	MetricApprovalPending = "ropeshift.approval.pending"
	MetricMessageState    = "ropeshift.message.state"
	MetricReplyLatency    = "ropeshift.message.reply.latency"
	MetricLeaseLapsed     = "ropeshift.lease.lapsed"
	MetricEffectDuration  = "ropeshift.effect.duration"
)

// The attributes of ropeshift's own spans and metrics, beside a lane's tags.
const (
	AttrTokenType = "ropeshift.token.type"
	// AttrAgent names the roster agent a figure counts toward.
	AttrAgent             = "ropeshift.agent"
	AttrOutcome           = "ropeshift.lane.outcome"
	AttrStartReason       = "ropeshift.generation.start_reason"
	AttrEndReason         = "ropeshift.generation.end_reason"
	AttrWorkOrderState    = "ropeshift.work_order.state"
	AttrWorkOrderKind     = "ropeshift.work_order.kind"
	AttrClaimResult       = "ropeshift.claim.result"
	AttrFence             = "ropeshift.fence"
	AttrParkTrigger       = "ropeshift.park.trigger"
	AttrApprovalKind      = "ropeshift.approval.kind"
	AttrApprovalResult    = "ropeshift.approval.result"
	AttrApprovalDecidedBy = "ropeshift.approval.decided_by"
	AttrQuestionRung      = "ropeshift.question.rung"
	AttrQuestionResult    = "ropeshift.question.result"
	AttrMessageID         = "ropeshift.message.id"
	AttrMessageKind       = "ropeshift.message.kind"
	AttrMessageState      = "ropeshift.message.state"
	AttrEffectKind        = "ropeshift.effect.kind"
	AttrEffectState       = "ropeshift.effect.state"
	AttrHandoffResult     = "ropeshift.handoff.result"
)

// TraceID is a trace's ID derived from what it is about, so every plane computes the same one with no
// message between them, and a span written after a crash still joins its trace.
func TraceID(parts ...string) string { return derivedID(16, parts) }

// WorkOrderTrace is the trace of one work order, which every span of its lanes joins.
func WorkOrderTrace(workOrder string) string { return TraceID("work_order", workOrder) }

// WorkOrderSpan is the span of the work order itself, the root of its trace.
func WorkOrderSpan(workOrder string) string { return SpanID("work_order", workOrder) }

// LaneSpan is the span of one lane.
func LaneSpan(lane string) string { return SpanID("lane", lane) }

// GenerationSpan is the span of one generation of a lane.
func GenerationSpan(lane string, generation int64) string {
	return SpanID("generation", lane, strconv.FormatInt(generation, 10))
}

// SpanID is a span's ID derived the same way.
func SpanID(parts ...string) string { return derivedID(8, parts) }

func derivedID(size int, parts []string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:size])
}
