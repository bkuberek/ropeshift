package practices

import (
	"strings"
	"testing"
	"time"
)

func TestPyprojectHasDependencies(t *testing.T) {
	for name, tt := range map[string]struct {
		text string
		want bool
	}{
		"an inline array":            {"[project]\ndependencies = [\"a\"]\n", true},
		"an empty array":             {"[project]\ndependencies = []\n", false},
		"a multi-line array":         {"[project]\ndependencies = [\n  \"a>=1\", # c\n]\n", true},
		"a multi-line empty array":   {"[project]\ndependencies = [\n  # nothing\n]\n[tool.x]\nk = \"v\"\n", false},
		"a comment holding a quote":  {"[project]\ndependencies = [\n  # \"a\"\n]\n", false},
		"a quote in a later table":   {"[project]\ndependencies = [\n]\n[tool.x]\nk = \"v\"\n", false},
		"optional dependencies only": {"[project]\n[project.optional-dependencies]\ndev = [\"pytest\"]\n", false},
		"poetry with a dependency":   {"[tool.poetry.dependencies]\npython = \"^3\"\nrequests = \"^2\"\n", true},
		"poetry with python only":    {"[tool.poetry.dependencies]\npython = \"^3\"\n", false},
		"a table after the array":    {"[project]\ndependencies = [\"a\"]\n[tool.poetry.dependencies]\npython = \"3\"\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := pyprojectHasDependencies(tt.text); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// A dependencies array that never closes is read once, line by line, so its size sets the time it takes.
func TestPyprojectScanOfAnUnterminatedArrayIsLinear(t *testing.T) {
	const size = 1 << 20
	head := "[project]\nname = \"x\"\ndependencies = [\n"
	for name, body := range map[string]string{
		"comment lines":   strings.Repeat("  # a comment line that holds no dependency\n", size/44),
		"opening bracket": strings.Repeat("[\n", size/2),
		"one long line":   strings.Repeat("[", size) + "\n",
		"blank lines":     strings.Repeat("\n", size),
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			if pyprojectHasDependencies(head + body) {
				t.Error("an array with no string reported a dependency")
			}
			if took := time.Since(start); took > 2*time.Second {
				t.Errorf("a %d byte array took %s", len(body), took)
			}
		})
	}
	t.Run("a string at the end is found", func(t *testing.T) {
		start := time.Now()
		if !pyprojectHasDependencies(head + strings.Repeat("  # c\n", size/6) + "  \"a\"\n") {
			t.Error("a dependency after a large array body was missed")
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("the scan took %s", took)
		}
	})
	t.Run("a string at the start ends the scan", func(t *testing.T) {
		if !pyprojectHasDependencies(head + "  \"a\",\n" + strings.Repeat("[", size)) {
			t.Error("a dependency at the start was missed")
		}
	})
}
