package lanes

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const spoolToken = "a-lanes-token"

func digestsOf(known map[string]string) func(string) (string, error) {
	return func(lane string) (string, error) { return known[lane], nil }
}

// handSealed is a line with any body at all, sealed the way a lane with this token can seal one.
func handSealed(token, hook string) []byte {
	return []byte(`{"hook":` + hook + `,"seal":"` + sealOf(TokenDigest(token), []byte(hook)) + `"}`)
}

func TestASealedLineOpens(t *testing.T) {
	event := HookEvent{Lane: "lane-1", Generation: 3, Event: "Stop", Subject: "s1", Digest: "sha256:abc", Message: "m <1> & 2", AtMs: 7}
	line, err := SealHook(spoolToken, event)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenHook(line, digestsOf(map[string]string{"lane-1": TokenDigest(spoolToken)}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Lane != "lane-1" || got.Generation != 3 || got.Event != "Stop" || got.Subject != "s1" || got.Digest != "sha256:abc" || got.Message != "m <1> & 2" {
		t.Errorf("event %+v", got)
	}
}

// A lane can read the spool, so a line holds nothing a lane could speak with.
func TestALineHoldsNoKey(t *testing.T) {
	line, err := SealHook(spoolToken, HookEvent{Lane: "lane-1", Generation: 1, Event: "Stop"})
	if err != nil {
		t.Fatal(err)
	}
	for name, secret := range map[string]string{"the token": spoolToken, "its digest": TokenDigest(spoolToken)} {
		if bytes.Contains(line, []byte(secret)) {
			t.Errorf("the line holds %s: %s", name, line)
		}
	}
	if !strings.HasSuffix(string(line), `"}`) || bytes.Contains(line, []byte("\n")) {
		t.Errorf("one write must be one line: %q", line)
	}
}

func TestAHookWithNoTokenSealsNothing(t *testing.T) {
	if _, err := SealHook("", HookEvent{Lane: "lane-1", Event: "Stop"}); !errors.Is(err, ErrSpoolLine) {
		t.Errorf("error %v", err)
	}
}

func TestALineThatProvesNothingSaysNothing(t *testing.T) {
	hook := `{"lane":"lane-1","generation":1,"event":"Stop","at_ms":1}`
	digests := map[string]string{"lane-1": TokenDigest(spoolToken), "other": TokenDigest("another-token"), "ended": ""}
	tests := []struct {
		name string
		line []byte
	}{
		{"not a line at all", []byte("not a line")},
		{"an event with no seal", []byte(hook)},
		{"a seal and no event", []byte(`{"seal":"00"}`)},
		{"another lane's seal", handSealed("another-token", hook)},
		{"a seal from no token", handSealed("", hook)},
		{"a seal of something else", []byte(`{"hook":` + hook + `,"seal":"` + sealOf(TokenDigest(spoolToken), []byte("{}")) + `"}`)},
		{"keys that fold to the right names", bytes.Replace(bytes.Replace(handSealed(spoolToken, hook), []byte(`"hook"`), []byte(`"HOOK"`), 1), []byte(`"seal"`), []byte(`"SEAL"`), 1)},
		{"a key that folds to the lane's", handSealed(spoolToken, `{"LANE":"lane-1","generation":1,"event":"Stop"}`)},
		{"a lane of no one", handSealed(spoolToken, `{"lane":"nobody","generation":1,"event":"Stop"}`)},
		{"a lane whose token ended", handSealed(spoolToken, `{"lane":"ended","generation":1,"event":"Stop"}`)},
		{"a line with no generation", handSealed(spoolToken, `{"lane":"lane-1","event":"Stop"}`)},
		{"a line with no event", handSealed(spoolToken, `{"lane":"lane-1","generation":1}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := OpenHook(tt.line, digestsOf(digests)); !errors.Is(err, ErrSpoolLine) {
				t.Errorf("event %+v, error %v", got, err)
			}
		})
	}
}

// A line can carry a second spelling of a key its seal does not read. The lane a seal proved is the
// lane the event belongs to, or a lane could seal a line of its own that lands on another.
func TestASecondSpellingOfALaneMovesNothing(t *testing.T) {
	digests := map[string]string{"mine": TokenDigest(spoolToken), "victim": TokenDigest("the-victims-token")}
	for name, hook := range map[string]string{
		"a fold after the key":  `{"lane":"mine","LANE":"victim","generation":1,"GENERATION":9,"event":"Stop"}`,
		"a fold before the key": `{"LANE":"victim","Lane":"victim","GENERATION":9,"lane":"mine","generation":1,"event":"Stop"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := OpenHook(handSealed(spoolToken, hook), digestsOf(digests))
			if err != nil {
				t.Fatal(err)
			}
			if got.Lane != "mine" || got.Generation != 1 {
				t.Errorf("event %+v", got)
			}
		})
	}
}

// A journal that cannot answer is not a line that proves nothing: the line stays for the next drain.
func TestAReadThatFailsIsNotARefusal(t *testing.T) {
	failure := errors.New("the journal is away")
	line, err := SealHook(spoolToken, HookEvent{Lane: "lane-1", Generation: 1, Event: "Stop"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = OpenHook(line, func(string) (string, error) { return "", failure })
	if !errors.Is(err, failure) || errors.Is(err, ErrSpoolLine) {
		t.Errorf("error %v", err)
	}
}
