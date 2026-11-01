package hook

import (
	"bytes"
	"strings"
	"testing"
)

func TestStatusMainPassesTheInputOn(t *testing.T) {
	input := `{"session_id":"s","context_window":{"current_usage":{"input_tokens":3}}}`
	var out bytes.Buffer
	if code := StatusMain([]string{flagPass}, strings.NewReader(input), &out, func(string) string { return "" }); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out.String() != input {
		t.Errorf("passed %q", out.String())
	}
}

func TestStatusMainPrintsNothingAlone(t *testing.T) {
	var out bytes.Buffer
	if code := StatusMain(nil, strings.NewReader("not json"), &out, func(string) string { return "" }); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out.Len() != 0 {
		t.Errorf("printed %q", out.String())
	}
}
