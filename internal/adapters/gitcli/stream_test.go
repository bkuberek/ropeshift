package gitcli_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/test/gittest"
)

func TestStreamPassesOutputToTheReader(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	first := repo.Commit("a.txt", "one\n")
	second := repo.Commit("b.txt", "two\n")
	var lines []string
	err := gitcli.Git{Dir: repo.Dir, Env: repo.Env()}.Stream(context.Background(), func(r io.Reader) error {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		return scanner.Err()
	}, "log", "--format=%H")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lines, ","); got != second+","+first {
		t.Errorf("the log reads %s", got)
	}
}

func TestStreamStopsWhenTheReaderFails(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("a.txt", strings.Repeat("x\n", 1000))
	stop := errors.New("enough")
	err := gitcli.Git{Dir: repo.Dir, Env: repo.Env()}.Stream(context.Background(), func(r io.Reader) error {
		return stop
	}, "log", "-p")
	if !errors.Is(err, stop) {
		t.Errorf("the error is %v, want the reader's", err)
	}
}

func TestStreamReportsAFailedCommand(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	err := gitcli.Git{Dir: repo.Dir, Env: repo.Env()}.Stream(context.Background(), func(r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		return err
	}, "log", "no-such-revision")
	if err == nil || !strings.Contains(err.Error(), "no-such-revision") {
		t.Errorf("the error is %v", err)
	}
}
