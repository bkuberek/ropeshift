package postgres_test

import (
	"math/rand/v2"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/ledger"
)

// TestEveryGeneratedIDGivesAGeneratedSchemaName holds that the shape the sweep drops is the shape Create
// gives a project whose ID it made, in the case this build writes and in the case an earlier build kept,
// across timestamps and entropy.
func TestEveryGeneratedIDGivesAGeneratedSchemaName(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		var entropy [ledger.ULIDEntropy]byte
		for i := range entropy {
			entropy[i] = byte(random.UintN(256))
		}
		id := ledger.EncodeULID(random.Int64N(1<<48), entropy)
		schema, err := postgres.SchemaName(ledger.WorkspaceID(id))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{schema, "project_" + id} {
			if !postgres.GeneratedSchema(name) {
				t.Fatalf("%s, from the ID %s, is not a generated schema name", name, id)
			}
		}
	}
}

// TestASchemaNameThatIsNotAGeneratedOneIsNotSwept holds the names a person might choose.
func TestASchemaNameThatIsNotAGeneratedOneIsNotSwept(t *testing.T) {
	const id = "01jabcdefghjkmnpqrstvwxyz0"
	for _, name := range []string{
		"",
		"project_",
		"project",
		"project_pa",
		"project_pa_backup",
		"project_" + id + "_backup",
		"project_" + id[1:],
		"project_" + id + "x",
		"project_" + id[:10] + strings.ToUpper(id[10:]),
		"project_" + strings.Replace(id, "a", "u", 1),
		"project_" + strings.Replace(id, "a", "-", 1),
		"project_8" + id[1:],
		"projects_" + id,
		"installation",
		"public",
	} {
		if postgres.GeneratedSchema(name) {
			t.Errorf("%q is taken for a generated schema name", name)
		}
	}
	for _, name := range []string{"project_" + id, "project_" + strings.ToUpper(id), "project_7" + id[1:]} {
		if !postgres.GeneratedSchema(name) {
			t.Errorf("%q is not taken for a generated schema name", name)
		}
	}
}
