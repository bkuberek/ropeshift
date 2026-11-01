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

// SabotageRecharge charges a crash again whenever a process gets to it.
const SabotageRecharge = "recharge"

func chargeKey(key string) string {
	if os.Getenv(EnvSabotage) != SabotageRecharge {
		return key
	}
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)
	return key + "/" + hex.EncodeToString(raw)
}
