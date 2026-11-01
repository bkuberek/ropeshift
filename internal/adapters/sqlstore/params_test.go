package sqlstore

import (
	"slices"
	"strconv"
	"testing"
)

func dollar(position int) string { return "$" + strconv.Itoa(position) }

func TestCompile(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		want  string
		names []string
	}{
		{"no parameters", "SELECT 1", "SELECT 1", nil},
		{"one parameter", "SELECT :a", "SELECT $1", []string{"a"}},
		{"a repeated name keeps its position", "SELECT :a, :b, :a", "SELECT $1, $2, $1", []string{"a", "b"}},
		{"a colon in a string stays", "SELECT ':a', :b", "SELECT ':a', $1", []string{"b"}},
		{"a cast stays", "SELECT 1::bigint, :a", "SELECT 1::bigint, $1", []string{"a"}},
		{"an underscore in a name", "SET x = :workspace_id", "SET x = $1", []string{"workspace_id"}},
		{"a colon before a digit stays", "SELECT ':', 1:2", "SELECT ':', 1:2", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compile(tt.text, dollar)
			if got.text != tt.want {
				t.Errorf("text %q, want %q", got.text, tt.want)
			}
			if len(got.names) != len(tt.names) {
				t.Fatalf("names %v, want %v", got.names, tt.names)
			}
			for i, name := range tt.names {
				if got.names[i] != name {
					t.Errorf("names %v, want %v", got.names, tt.names)
				}
			}
		})
	}
}

func TestBindRefusesAMissingName(t *testing.T) {
	c := compile("SELECT :a, :b", dollar)
	if _, err := c.bind(params{"a": 1}); err == nil {
		t.Fatal("bind accepted a missing parameter")
	}
	args, err := c.bind(params{"b": 2, "a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if args[0] != 1 || args[1] != 2 {
		t.Errorf("args %v", args)
	}
}

func TestSplitStatements(t *testing.T) {
	text := `-- A comment; with a mark.
CREATE TABLE a (id TEXT);

CREATE TRIGGER a_insert AFTER INSERT ON a BEGIN
  INSERT INTO b VALUES (new.id);
  INSERT INTO c VALUES (new.id);
END;

CREATE INDEX a_id ON a (id);
`
	got := splitStatements(text)
	want := []string{
		"CREATE TABLE a (id TEXT)",
		"CREATE TRIGGER a_insert AFTER INSERT ON a BEGIN\n  INSERT INTO b VALUES (new.id);\n  INSERT INTO c VALUES (new.id);\nEND",
		"CREATE INDEX a_id ON a (id)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}
