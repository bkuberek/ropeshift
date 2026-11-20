package questions

import (
	"errors"
	"testing"
)

func TestALeftOutPathsQuestionReadsBackItsRepositoryAndPath(t *testing.T) {
	t.Parallel()
	question, err := LeftFile("files-1", "repo-1", ".atl/")
	if err != nil {
		t.Fatal(err)
	}
	repository, path, ok := ReadLeftFile(question)
	if !ok || repository != "repo-1" || path != ".atl/" {
		t.Errorf("read %q %q %v", repository, path, ok)
	}
	if _, _, ok := ReadLeftFile(Question{Text: question.Text}); ok {
		t.Error("a question outside the files category read as a left-out path's")
	}
	for statement, want := range map[string]string{LeftFileCommitOption(".atl/"): LeftFileKeep, LeftFileIgnoreOption(".atl/"): LeftFileIgnore} {
		if got, ok := LeftFileChoice(question, statement); !ok || got != want {
			t.Errorf("%q chose %q %v, want %q", statement, got, ok, want)
		}
	}
	if _, ok := LeftFileChoice(question, "Keep it somewhere"); ok {
		t.Error("a statement that is neither option chose one")
	}
}

// TestARulingOnALeftOutPathIsOneOfItsOptions holds an orchestrator's ruling to what a worker can act on: one of the
// two options, scoped to the repository when it names no scope.
func TestARulingOnALeftOutPathIsOneOfItsOptions(t *testing.T) {
	t.Parallel()
	question, err := LeftFile("files-1", "repo-1", ".atl/")
	if err != nil {
		t.Fatal(err)
	}
	decision := Decision{Statement: LeftFileIgnoreOption(".atl/")}
	if err := CheckLeftFileRuling(question, &decision); err != nil || decision.Scope != "repo:repo-1" {
		t.Errorf("a ruling on an option: scope %q, %v", decision.Scope, err)
	}
	if err := CheckLeftFileRuling(question, &Decision{Statement: "Leave it"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a ruling on no option: %v", err)
	}
	if err := CheckLeftFileRuling(Question{Text: "Which logo?"}, &Decision{Statement: "Leave it"}); err != nil {
		t.Errorf("another question's ruling: %v", err)
	}
}
