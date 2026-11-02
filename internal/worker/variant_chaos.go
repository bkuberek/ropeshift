//go:build chaos

package worker

import (
	"crypto/rand"
	"encoding/hex"
	"os"
)

// EnvSabotage names a broken variant of the worker, to prove that a check can fail. The
// chaos build holds this seam, and a normal build has none.
const EnvSabotage = "ROPESHIFT_CHAOS_WORKER"

// The broken variants of a worker.
const (
	// SabotageRecharge charges a crash again whenever a process gets to it.
	SabotageRecharge = "recharge"
	// SabotageNoGuard reads every worktree as clean.
	SabotageNoGuard = "no-guard"
	// SabotageKeepResources never gives a block back.
	SabotageKeepResources = "keep-resources"
	// SabotageBlindRemoval removes a worktree with no second reading.
	SabotageBlindRemoval = "blind-removal"
	// SabotageStoresAgain stores a handoff again, whatever the journal holds.
	SabotageStoresAgain = "stores-again"
	// SabotageForgetsHandoff ends a lane whose handoff lost its session.
	SabotageForgetsHandoff = "forgets-handoff"
	// SabotageResendsHandoff gives each process its own instruction.
	SabotageResendsHandoff = "resends-handoff"
	// SabotageSplitsHandoff keeps a document and its upload in two journal writes.
	SabotageSplitsHandoff = "splits-handoff"
	// SabotageParksFirst commits `parked` ahead of the stop.
	SabotageParksFirst = "parks-first"
	// SabotageForgetsWake ends a lane whose parking lost its session.
	SabotageForgetsWake = "forgets-wake"
	// SabotageSplitsPark keeps a parked commit and its upload in two journal writes.
	SabotageSplitsPark = "splits-park"
	// SabotageSplitsResume keeps a resume and its upload in two journal writes.
	SabotageSplitsResume = "splits-resume"
	// SabotageLeavesParked journals a resume with its lane still reading `parked`.
	SabotageLeavesParked = "leaves-parked"
	// SabotageResendsMessage gives each process its own write of a message.
	SabotageResendsMessage = "resends-message"
	// SabotageSplitsMessage keeps a message's write and what it proves in two journal writes.
	SabotageSplitsMessage = "splits-message"
)

func sabotaged(name string) bool { return os.Getenv(EnvSabotage) == name }

func skipsGuard() bool { return sabotaged(SabotageNoGuard) }

func keepsResources() bool { return sabotaged(SabotageKeepResources) }

func removesBlind() bool { return sabotaged(SabotageBlindRemoval) }

func storesAgain() bool { return sabotaged(SabotageStoresAgain) }

func forgetsHandoff() bool { return sabotaged(SabotageForgetsHandoff) }

func splitsHandoff() bool { return sabotaged(SabotageSplitsHandoff) }

func parksFirst() bool { return sabotaged(SabotageParksFirst) }

func forgetsWake() bool { return sabotaged(SabotageForgetsWake) }

func splitsPark() bool { return sabotaged(SabotageSplitsPark) }

func splitsResume() bool { return sabotaged(SabotageSplitsResume) }

func leavesParked() bool { return sabotaged(SabotageLeavesParked) }

func splitsMessage() bool { return sabotaged(SabotageSplitsMessage) }

func messageKey(id string) string {
	if !sabotaged(SabotageResendsMessage) {
		return messageLabel + id
	}
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)
	return messageLabel + id + "/" + hex.EncodeToString(raw)
}

func instructionLabel() string {
	if !sabotaged(SabotageResendsHandoff) {
		return handoffInput
	}
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)
	return handoffInput + "/" + hex.EncodeToString(raw)
}

func chargeKey(key string) string {
	if os.Getenv(EnvSabotage) != SabotageRecharge {
		return key
	}
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)
	return key + "/" + hex.EncodeToString(raw)
}
