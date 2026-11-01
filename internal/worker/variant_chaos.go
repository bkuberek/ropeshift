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
)

func sabotaged(name string) bool { return os.Getenv(EnvSabotage) == name }

func skipsGuard() bool { return sabotaged(SabotageNoGuard) }

func keepsResources() bool { return sabotaged(SabotageKeepResources) }

func removesBlind() bool { return sabotaged(SabotageBlindRemoval) }

func chargeKey(key string) string {
	if os.Getenv(EnvSabotage) != SabotageRecharge {
		return key
	}
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)
	return key + "/" + hex.EncodeToString(raw)
}
