package ledger

import "testing"

func TestCreationKeyIsStableAndNamesTheApprovalAndTheEntry(t *testing.T) {
	base := CreationKey("approval-1", "api")
	if again := CreationKey("approval-1", "api"); again != base {
		t.Errorf("the key of one approval and name reads %q, then %q", base, again)
	}
	if len(base) != 64 || base != lowerHex(base) {
		t.Errorf("the key %q is not 64 lowercase hex characters", base)
	}
	for name, other := range map[string]string{
		"another approval":      CreationKey("approval-2", "api"),
		"another name":          CreationKey("approval-1", "web"),
		"a name moved into it":  CreationKey("approval-1a", "pi"),
		"the pair run together": CreationKey("approval-1api", ""),
	} {
		if other == base {
			t.Errorf("%s has the key of the first pair", name)
		}
	}
}

func lowerHex(text string) string {
	out := []byte(text)
	for i, c := range out {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			out[i] = '?'
		}
	}
	return string(out)
}
