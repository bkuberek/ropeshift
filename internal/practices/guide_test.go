package practices_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/practices"
)

// guidePath is the Best practices user guide, which prints the practice table and the package manager table for
// people to read (FR-PRAC-12).
var guidePath = filepath.Join("..", "..", "docs", "best-practices.md")

// TestTheBestPracticesGuideHoldsThePracticeTables keeps the guide in step with the binary's tables: each practice
// has its own section with its severity, applicability, why, fix, and template, and the package manager table names
// every manager, manifest, lockfile, lock command, and Dependabot ecosystem.
func TestTheBestPracticesGuideHoldsThePracticeTables(t *testing.T) {
	guide := readGuide(t, guidePath)
	for _, problem := range guideProblems(guide) {
		t.Errorf("%s: %s", guidePath, problem)
	}

	t.Run("a copy without one practice's section fails", func(t *testing.T) {
		for _, p := range practices.Table() {
			t.Run(string(p.ID), func(t *testing.T) {
				heading := "### `" + string(p.ID) + "`"
				broken := withoutSection(t, guide, heading)
				problems := guideProblems(writeCopy(t, broken))
				want := fmt.Sprintf("no section %s", heading)
				if !slices.Contains(problems, want) {
					t.Errorf("the copy without %s reports %q, want a problem %q", heading, problems, want)
				}
			})
		}
	})

	t.Run("a copy without one lockfile fails", func(t *testing.T) {
		broken := strings.Replace(guide, "`Cargo.lock`", "`Cargo.toml`", 1)
		if broken == guide {
			t.Fatal("the guide names no `Cargo.lock` to drop")
		}
		problems := guideProblems(writeCopy(t, broken))
		want := "the package manager row of `cargo` lacks `Cargo.lock`"
		if !slices.Contains(problems, want) {
			t.Errorf("the copy without Cargo.lock reports %q, want a problem %q", problems, want)
		}
	})
}

// guideProblems says what the guide lacks of the binary's tables, one problem to a line.
func guideProblems(guide string) []string {
	var problems []string
	for _, p := range practices.Table() {
		heading := "### `" + string(p.ID) + "`"
		body, ok := section(guide, heading)
		if !ok {
			problems = append(problems, "no section "+heading)
			continue
		}
		want := []string{
			"Severity: `" + string(p.Severity) + "`",
			"Applies when: " + p.Applies,
			"Why it matters: " + p.Why,
			"Fix: " + p.Fix,
		}
		if p.Template == "" {
			want = append(want, "Template: none")
		} else {
			want = append(want, "`"+p.Command()+"`", "`/templates?name="+p.Template+"`")
		}
		for _, text := range want {
			if !strings.Contains(body, text) {
				problems = append(problems, fmt.Sprintf("the section %s lacks %q", heading, text))
			}
		}
	}

	managers, ok := section(guide, "## Package managers")
	if !ok {
		return append(problems, "no section ## Package managers")
	}
	for _, segment := range practices.SkippedSegments() {
		if !strings.Contains(managers, "`"+segment+"`") {
			problems = append(problems, fmt.Sprintf("the package managers section lacks the skipped folder `%s`", segment))
		}
	}
	for _, m := range practices.Managers() {
		row, ok := tableRow(managers, "`"+m.Name+"`")
		if !ok {
			problems = append(problems, fmt.Sprintf("the package manager table has no row for `%s`", m.Name))
			continue
		}
		cells := slices.Concat(m.Manifests, m.Lockfiles, []string{m.Ecosystem})
		if m.LockCommand != "" {
			cells = append(cells, m.LockCommand)
		}
		for _, command := range m.ManifestLockCommands {
			cells = append(cells, command)
		}
		for _, cell := range cells {
			if !strings.Contains(row, "`"+cell+"`") {
				problems = append(problems, fmt.Sprintf("the package manager row of `%s` lacks `%s`", m.Name, cell))
			}
		}
		if m.NoLockfile != "" && !strings.Contains(managers, m.NoLockfile) {
			problems = append(problems, fmt.Sprintf("the package managers section lacks %q", m.NoLockfile))
		}
	}

	ids, ok := section(guide, "## Finding IDs")
	if !ok {
		return append(problems, "no section ## Finding IDs")
	}
	examples := 0
	for _, line := range strings.Split(ids, "\n") {
		example, ok := lastCodeCell(line)
		if !ok {
			continue
		}
		examples++
		if _, err := practices.ParseFindingID(example); err != nil {
			problems = append(problems, fmt.Sprintf("the finding ID example `%s` does not parse: %v", example, err))
		}
	}
	if examples == 0 {
		problems = append(problems, "the finding IDs section has no example")
	}
	return problems
}

// section returns the text under a heading, up to the next heading of the same or a higher level, outside code
// blocks.
func section(guide, heading string) (string, bool) {
	level := headingLevel(heading)
	var body []string
	inside, fenced := false, false
	for _, line := range strings.Split(guide, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		if !fenced {
			if l := headingLevel(line); l > 0 {
				switch {
				case strings.TrimSpace(line) == heading:
					inside = true
					continue
				case inside && l <= level:
					return strings.Join(body, "\n"), true
				}
			}
		}
		if inside {
			body = append(body, line)
		}
	}
	return strings.Join(body, "\n"), inside
}

// headingLevel is the number of #s a Markdown heading line starts with, and 0 for a line that is no heading.
func headingLevel(line string) int {
	n := len(line) - len(strings.TrimLeft(line, "#"))
	if n == 0 || n > 6 || !strings.HasPrefix(line[n:], " ") {
		return 0
	}
	return n
}

// tableRow finds the table row whose first cell is the text.
func tableRow(body, first string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "| "+first+" |") {
			return line, true
		}
	}
	return "", false
}

// lastCodeCell returns the code in the last cell of a table row, when that cell is one code span.
func lastCodeCell(line string) (string, bool) {
	if !strings.HasPrefix(line, "|") {
		return "", false
	}
	cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	last := strings.TrimSpace(cells[len(cells)-1])
	code, ok := strings.CutPrefix(last, "`")
	if !ok || !strings.HasSuffix(code, "`") || strings.Contains(strings.TrimSuffix(code, "`"), "`") {
		return "", false
	}
	return strings.TrimSuffix(code, "`"), true
}

// withoutSection drops a heading and the text under it, as an editor deleting the section would.
func withoutSection(t *testing.T, guide, heading string) string {
	t.Helper()
	body, ok := section(guide, heading)
	if !ok {
		t.Fatalf("the guide has no section %s", heading)
	}
	whole := heading + "\n" + body
	if !strings.Contains(guide, whole) {
		t.Fatalf("the section %s is not one block of the guide", heading)
	}
	return strings.Replace(guide, whole, "", 1)
}

// writeCopy writes a copy of the guide to a temporary folder and reads it back, as the test reads the real one.
func writeCopy(t *testing.T, guide string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "best-practices.md")
	if err := os.WriteFile(path, []byte(guide), 0o600); err != nil {
		t.Fatal(err)
	}
	return readGuide(t, path)
}

func readGuide(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the Best practices guide: %v", err)
	}
	return string(raw)
}
