package questions

import (
	"errors"
	"strconv"
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

// TestOnlyTheControlPlaneGivesALeftOutPathsQuestionItsID refuses an asker's ID that begins as a left-out path's
// question's does, and reads a question with the same text under another ID as no left-out path's.
func TestOnlyTheControlPlaneGivesALeftOutPathsQuestionItsID(t *testing.T) {
	t.Parallel()
	if err := CheckAskedID(LeftFileID("report", ".atl/")); !errors.Is(err, ErrInvalid) {
		t.Errorf("an asker gave a left-out path's ID: %v", err)
	}
	if err := CheckAskedID("q-1"); err != nil {
		t.Errorf("an asker's own ID: %v", err)
	}
	question, err := LeftFile("files-1", "repo-1", "src/")
	if err != nil {
		t.Fatal(err)
	}
	question.ID = "q-1"
	if _, _, ok := ReadLeftFile(question); ok {
		t.Error("a question under an asker's ID read as a left-out path's")
	}
}

// TestALeftOutPathHoldsNoLineBreakOrNUL refuses a path that would be more than one line in a .gitignore or a report,
// such as "x\n*", which would add `*`, both when its question is made and when one is read back.
func TestALeftOutPathHoldsNoLineBreakOrNUL(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"x\n*", "x\r*", "x\x00y"} {
		if _, err := LeftFile("files-1", "repo-1", path); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q made a question: %v", path, err)
		}
		crafted := Question{
			ID: "files-1", Categories: []string{CategoryFiles},
			Text: leftFileOpening + strconv.Quote(path) + leftFileRepository + "repo-1" + leftFileClosing + "Decide it.",
		}
		if _, read, ok := ReadLeftFile(crafted); ok {
			t.Errorf("a question read back %q", read)
		}
	}
}

// TestAGitignoreIsNeverRuledIgnored refuses a ruling that would ignore the file that says what git ignores, and
// takes one that keeps it.
func TestAGitignoreIsNeverRuledIgnored(t *testing.T) {
	t.Parallel()
	for _, path := range []string{".gitignore", "app/.gitignore"} {
		question, err := LeftFile("files-1", "repo-1", path)
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckLeftFileRuling(question, &Decision{Statement: LeftFileIgnoreOption(path)}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s was ruled ignored: %v", path, err)
		}
		if err := CheckLeftFileRuling(question, &Decision{Statement: LeftFileCommitOption(path)}); err != nil {
			t.Errorf("%s was not ruled kept: %v", path, err)
		}
	}
}
