package contract

import (
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
)

func TestForgeContract(t *testing.T) {
	RunForge(t, func(*testing.T) ForgeRig {
		forge := &fake.Forge{}
		return ForgeRig{Forge: forge, Scope: "workspace-a", Set: forge.Set}
	})
}
