package contract

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
)

func TestForgeContract(t *testing.T) {
	t.Parallel()
	RunForge(t, func(*testing.T) ForgeRig {
		forge := &fake.Forge{}
		return ForgeRig{Forge: forge, Scope: "workspace-a", Set: forge.Set}
	})
}
