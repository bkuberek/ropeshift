package lanes

import (
	"os"
	"path/filepath"
	"testing"
)

func laneReadPolicy() DenyPolicy {
	return CompilePolicy(PolicySpec{
		Worktree: "/work/lane-1", Home: "/home/person",
		ReadOnlyRoots: []string{"/work/lane-1.reads/api"},
		AskReadRoots:  []string{"/work/lane-1", "/work/lane-1.reads/api", "/run/lane-1"},
	})
}

// A lane's file tool and the shell reads the hook can see ask when they reach past the worktree, the read-only
// trees, and the lane's private directory, and read nothing else for a person.
func TestAReadOutsideALanesRootsAsks(t *testing.T) {
	tests := []struct {
		name string
		call ToolCall
		asks bool
	}{
		{"read in /tmp", ToolCall{Tool: "Read", Path: "/tmp/notes.txt", Cwd: "/work/lane-1"}, true},
		{"read under the home directory", ToolCall{Tool: "Read", Path: "/home/person/.ssh/id_ed25519", Cwd: "/work/lane-1"}, true},
		{"read written with a tilde", ToolCall{Tool: "Read", Path: "~/.ssh/config", Cwd: "/work/lane-1"}, true},
		{"read that climbs out", ToolCall{Tool: "Read", Path: "../../etc/passwd", Cwd: "/work/lane-1"}, true},
		{"search in /tmp", ToolCall{Tool: "Grep", Path: "/tmp", Cwd: "/work/lane-1"}, true},
		{"listing of the home directory", ToolCall{Tool: "LS", Path: "/home/person", Cwd: "/work/lane-1"}, true},
		{"glob that starts outside", ToolCall{Tool: "Glob", Pattern: "/tmp/**/*.go", Cwd: "/work/lane-1"}, true},
		{"glob that climbs out", ToolCall{Tool: "Glob", Pattern: "../other/*.go", Cwd: "/work/lane-1"}, true},
		{"read in the worktree", ToolCall{Tool: "Read", Path: "/work/lane-1/main.go", Cwd: "/work/lane-1"}, false},
		{"read by a relative path", ToolCall{Tool: "Read", Path: "main.go", Cwd: "/work/lane-1"}, false},
		{"read in a read-only tree", ToolCall{Tool: "Read", Path: "/work/lane-1.reads/api/handler.go", Cwd: "/work/lane-1"}, false},
		{"read in the lane's private directory", ToolCall{Tool: "Read", Path: "/run/lane-1/note", Cwd: "/work/lane-1"}, false},
		{"search with no path", ToolCall{Tool: "Grep", Cwd: "/work/lane-1/internal"}, false},
		{"glob in the worktree", ToolCall{Tool: "Glob", Pattern: "**/*.go", Cwd: "/work/lane-1"}, false},
		{"write is no read", ToolCall{Tool: "Write", Path: "/tmp/x", Cwd: "/work/lane-1"}, false},

		{"cat of a file in /etc", ToolCall{Tool: "Bash", Command: "cat /etc/hosts", Cwd: "/work/lane-1"}, true},
		{"cat of a file in the home directory", ToolCall{Tool: "Bash", Command: "cat ~/.ssh/config", Cwd: "/work/lane-1"}, true},
		{"head of a file by $HOME", ToolCall{Tool: "Bash", Command: "head -n 5 $HOME/.bashrc", Cwd: "/work/lane-1"}, true},
		{"search of /tmp", ToolCall{Tool: "Bash", Command: "rg TODO /tmp", Cwd: "/work/lane-1"}, true},
		{"listing after a cd out", ToolCall{Tool: "Bash", Command: "cd /tmp && ls", Cwd: "/work/lane-1"}, true},
		{"listing of the home directory", ToolCall{Tool: "Bash", Command: "ls ~", Cwd: "/work/lane-1"}, true},
		{"find from outside", ToolCall{Tool: "Bash", Command: "find /var/log -name '*.log'", Cwd: "/work/lane-1"}, true},
		{"input redirect from outside", ToolCall{Tool: "Bash", Command: "wc -l < /etc/hosts", Cwd: "/work/lane-1"}, true},
		{"read inside a nested shell", ToolCall{Tool: "Bash", Command: "sh -c 'cat /etc/hosts'", Cwd: "/work/lane-1"}, true},
		{"read behind a pipe", ToolCall{Tool: "Bash", Command: "echo hi | tee x; cat /etc/hosts | wc -l", Cwd: "/work/lane-1"}, true},
		{"read that climbs out", ToolCall{Tool: "Bash", Command: "cat ../../etc/hosts", Cwd: "/work/lane-1"}, true},
		{"read by a monitor", ToolCall{Tool: "Monitor", Command: "tail -f /var/log/syslog", Cwd: "/work/lane-1"}, true},
		{"cat in the worktree", ToolCall{Tool: "Bash", Command: "cat /work/lane-1/main.go", Cwd: "/work/lane-1"}, false},
		{"cat of a relative file", ToolCall{Tool: "Bash", Command: "cat README.md", Cwd: "/work/lane-1"}, false},
		{"cat in a read-only tree", ToolCall{Tool: "Bash", Command: "head -n 5 /work/lane-1.reads/api/handler.go", Cwd: "/work/lane-1"}, false},
		{"search with a pattern that looks like a path", ToolCall{Tool: "Bash", Command: "rg /api/v1 internal", Cwd: "/work/lane-1"}, false},
		{"listing of the worktree", ToolCall{Tool: "Bash", Command: "ls", Cwd: "/work/lane-1"}, false},
		{"listing after a cd inside", ToolCall{Tool: "Bash", Command: "cd internal && ls", Cwd: "/work/lane-1"}, false},
		{"a place only a shell can name", ToolCall{Tool: "Bash", Command: "cd \"$(git rev-parse --show-toplevel)\" && ls", Cwd: "/work/lane-1"}, false},
		{"a path a variable holds", ToolCall{Tool: "Bash", Command: "cat $FILE", Cwd: "/work/lane-1"}, true},
		{"a path a substitution makes", ToolCall{Tool: "Bash", Command: "cat $(echo f)", Cwd: "/work/lane-1"}, true},
		{"a path a quoted variable holds", ToolCall{Tool: "Bash", Command: "rg token \"$DIR\"", Cwd: "/work/lane-1"}, true},
		{"a pattern a variable holds", ToolCall{Tool: "Bash", Command: "rg \"$PATTERN\" internal", Cwd: "/work/lane-1"}, false},
		{"paths a pipe hands to a reader", ToolCall{Tool: "Bash", Command: "echo f | xargs cat", Cwd: "/work/lane-1"}, true},
		{"paths a pipe hands to a reader behind options", ToolCall{Tool: "Bash", Command: "git ls-files | xargs -n 1 wc -l", Cwd: "/work/lane-1"}, true},
		{"a pipe into a program that is no reader", ToolCall{Tool: "Bash", Command: "git ls-files | xargs echo", Cwd: "/work/lane-1"}, false},
		{"a copy from outside", ToolCall{Tool: "Bash", Command: "cp /etc/passwd ./p", Cwd: "/work/lane-1"}, true},
		{"a copy of several from outside", ToolCall{Tool: "Bash", Command: "cp -r a /var/log b", Cwd: "/work/lane-1"}, true},
		{"a copy inside", ToolCall{Tool: "Bash", Command: "cp -r a b", Cwd: "/work/lane-1"}, false},
		{"a copy to a directory named by an option", ToolCall{Tool: "Bash", Command: "cp -t dest /etc/hosts", Cwd: "/work/lane-1"}, true},
		{"an install from outside", ToolCall{Tool: "Bash", Command: "install -m 644 /etc/hosts ./h", Cwd: "/work/lane-1"}, true},
		{"a sync from outside", ToolCall{Tool: "Bash", Command: "rsync -a /etc/ ./x/", Cwd: "/work/lane-1"}, true},
		{"a sync inside", ToolCall{Tool: "Bash", Command: "rsync -a src/ dst/", Cwd: "/work/lane-1"}, false},
		{"an archive of a place outside", ToolCall{Tool: "Bash", Command: "tar -cf - /etc", Cwd: "/work/lane-1"}, true},
		{"an archive with no dash", ToolCall{Tool: "Bash", Command: "tar cf - /etc", Cwd: "/work/lane-1"}, true},
		{"an archive of the worktree", ToolCall{Tool: "Bash", Command: "tar -czf out.tgz internal", Cwd: "/work/lane-1"}, false},
		{"python code that names a file outside", ToolCall{Tool: "Bash", Command: "python3 -c 'print(open(\"/etc/passwd\").read())'", Cwd: "/work/lane-1"}, true},
		{"python code that names the home directory", ToolCall{Tool: "Bash", Command: "python3 -c 'import os; os.listdir(\"~/.ssh\")'", Cwd: "/work/lane-1"}, true},
		{"node code that names a file outside", ToolCall{Tool: "Bash", Command: "node -e \"require('fs').readFileSync('/etc/hosts')\"", Cwd: "/work/lane-1"}, true},
		{"perl code that names a file outside", ToolCall{Tool: "Bash", Command: "perl -e 'open F, \"</etc/passwd\"'", Cwd: "/work/lane-1"}, true},
		{"ruby code that names a file outside", ToolCall{Tool: "Bash", Command: "ruby -e 'File.read(\"/etc/hosts\")'", Cwd: "/work/lane-1"}, true},
		{"python code that climbs out", ToolCall{Tool: "Bash", Command: "python3 -c 'open(\"../../etc/passwd\")'", Cwd: "/work/lane-1"}, true},
		{"python code a variable holds", ToolCall{Tool: "Bash", Command: "python3 -c \"$CODE\"", Cwd: "/work/lane-1"}, true},
		{"python code a here-document holds", ToolCall{Tool: "Bash", Command: "python3 <<'EOF'\nprint(open('/etc/passwd').read())\nEOF", Cwd: "/work/lane-1"}, true},
		{"python code that names a file inside", ToolCall{Tool: "Bash", Command: "python3 -c 'print(open(\"/work/lane-1/main.py\").read())'", Cwd: "/work/lane-1"}, false},
		{"python code that names no path", ToolCall{Tool: "Bash", Command: "python3 -c 'print(1 + 1)'", Cwd: "/work/lane-1"}, false},
		{"a script of the worktree", ToolCall{Tool: "Bash", Command: "python3 scripts/check.py", Cwd: "/work/lane-1"}, false},
		{"a module", ToolCall{Tool: "Bash", Command: "python3 -m pytest", Cwd: "/work/lane-1"}, false},
		{"input redirect from the null device", ToolCall{Tool: "Bash", Command: "go test ./... < /dev/null", Cwd: "/work/lane-1"}, false},
		{"a build", ToolCall{Tool: "Bash", Command: "go test ./... && go build ./...", Cwd: "/work/lane-1"}, false},
		{"a write to /tmp is the write rule's", ToolCall{Tool: "Bash", Command: "echo hi > /tmp/x", Cwd: "/work/lane-1"}, false},
	}
	policy := laneReadPolicy()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, asks := policy.Asks(tt.call)
			if asks != tt.asks {
				t.Errorf("Asks(%+v) = %q, %v; want asks %v", tt.call, reason, asks, tt.asks)
			}
			if asks && reason != ReasonAskReadOutside && reason != ReasonAskRead {
				t.Errorf("reason %q", reason)
			}
		})
	}
}

// A policy with no ask roots asks for no read, so a session that holds none reads as before.
func TestAPolicyWithNoAskReadRootsAsksForNoRead(t *testing.T) {
	policy := CompilePolicy(PolicySpec{Worktree: "/work/lane-1", Home: "/home/person"})
	for _, call := range []ToolCall{
		{Tool: "Read", Path: "/tmp/x", Cwd: "/work/lane-1"},
		{Tool: "Bash", Command: "cat /etc/hosts", Cwd: "/work/lane-1"},
	} {
		if reason, asks := policy.Asks(call); asks {
			t.Errorf("%+v asks: %q", call, reason)
		}
	}
}

// A read asks by where its path leads: a link in the worktree that leads out asks, a path with nothing there yet
// does not, and a path inside a root reads as inside.
func TestAReadAsksByWhereItLeads(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(base, "lane")
	outside := filepath.Join(base, "elsewhere")
	for _, dir := range []string{worktree, outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(worktree, "out")); err != nil {
		t.Fatal(err)
	}
	policy := CompilePolicy(PolicySpec{Worktree: worktree, AskReadRoots: []string{worktree}}).WithRealPaths(filepath.EvalSymlinks)
	tests := []struct {
		name string
		call ToolCall
		asks bool
	}{
		{"a link that leads out", ToolCall{Tool: "Read", Path: filepath.Join(worktree, "out", "secret"), Cwd: worktree}, true},
		{"a link read by a shell", ToolCall{Tool: "Bash", Command: "cat out/secret", Cwd: worktree}, true},
		{"a file not made yet", ToolCall{Tool: "Read", Path: filepath.Join(worktree, "later.go"), Cwd: worktree}, false},
		{"a file inside", ToolCall{Tool: "Read", Path: filepath.Join(worktree, "main.go"), Cwd: worktree}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, asks := policy.Asks(tt.call); asks != tt.asks {
				t.Errorf("asks = %v, want %v", asks, tt.asks)
			}
		})
	}
}

// A session held to one program has no read to ask for: its read roots deny, and its shell runs nothing else.
func TestAnAssistantsReadsStayDenied(t *testing.T) {
	policy := CompilePolicy(PolicySpec{
		Worktree: "/run/a/session", ReadRoots: []string{"/run/a/session"}, Program: "/bin/ropeshift", Commands: []string{"lane list"},
	})
	call := ToolCall{Tool: "Read", Path: "/tmp/x", Cwd: "/run/a/session"}
	if reason, denied := policy.Decide(call); !denied || reason != ReasonReadOutside {
		t.Errorf("Decide = %q, %v", reason, denied)
	}
	if reason, asks := policy.Asks(call); asks {
		t.Errorf("asks %q", reason)
	}
}

// A shell call that runs ln asks, because a link made in the call leads where the rest of the call reads, and the file
// behind it does not exist when the hook decides.
func TestACallThatMakesALinkAsks(t *testing.T) {
	policy := laneReadPolicy()
	for _, command := range []string{
		"ln -s /etc e && cat e/passwd", "ln -s ../x y", "ln a b", "command ln -s /etc e", "env ln -s /etc e", "sh -c 'ln -s /etc e'",
		"sudo ln -sf /etc e", "echo hi; ln -s /etc e",
	} {
		if reason, asks := policy.Asks(ToolCall{Tool: "Bash", Command: command, Cwd: "/work/lane-1"}); !asks {
			t.Errorf("%q asks nothing (%q)", command, reason)
		}
	}
	if _, asks := policy.Asks(ToolCall{Tool: "Bash", Command: "echo ln -s a b", Cwd: "/work/lane-1"}); asks {
		t.Error("a word that is no command asks")
	}
}

// The operands after the code an interpreter is given inline are the files it reads with -n and -p, so they read as paths.
func TestTheFilesAnInlineScriptReadsAreReadPaths(t *testing.T) {
	policy := laneReadPolicy()
	tests := []struct {
		command string
		asks    bool
	}{
		{"perl -ne print /etc/passwd", true},
		{"perl -pe 's/a/b/' /etc/hosts", true},
		{"ruby -ne 'puts $_' /etc/hosts", true},
		{"perl -e 'print 1' ~/.ssh/config", true},
		{"awk 'END{print NR}' /etc/passwd", true},
		{"awk -F: '{print $1}' /etc/passwd", true},
		{"perl -ne print main.go", false},
		{"perl -ne print /work/lane-1/main.go", false},
		{"ruby -ne 'puts $_' internal/a.rb internal/b.rb", false},
		{"awk 'END{print NR}' main.go", false},
	}
	for _, tt := range tests {
		if _, asks := policy.Asks(ToolCall{Tool: "Bash", Command: tt.command, Cwd: "/work/lane-1"}); asks != tt.asks {
			t.Errorf("%q: asks = %v, want %v", tt.command, asks, tt.asks)
		}
	}
}

// What an agent runs all day asks nothing when it reads inside: a pipe into xargs from a listing rooted inside that
// lists no link, a loop over words that name inside, and a variable assigned a static word. Each near miss still asks.
func TestOrdinaryReadsInsideAskNothing(t *testing.T) {
	inside := CompilePolicy(PolicySpec{
		Worktree: "/work/lane-1", Home: "/home/person", AskReadRoots: []string{"/work/lane-1", "/work/lane-1.reads/api"}, GitListingsInside: true,
	})
	linked := CompilePolicy(PolicySpec{Worktree: "/work/lane-1", Home: "/home/person", AskReadRoots: []string{"/work/lane-1"}})
	tests := []struct {
		name    string
		command string
		asks    bool
	}{
		{"a git listing into a reader", "git ls-files | xargs wc -l", false},
		{"a git listing into a reader behind options", "git ls-files | xargs -n 1 wc -l", false},
		{"a git diff listing", "git diff --name-only | xargs head -5", false},
		{"a git grep listing", "git grep -l TODO | xargs sed -n 1p", false},
		{"a search listing into sed", "rg -l X | xargs sed -n 1p", false},
		{"a search listing of the files", "rg --files | xargs wc -l", false},
		{"a search listing rooted inside", "rg -l X internal | xargs cat", false},
		{"a find of files", "find . -type f -name '*.go' | xargs grep -l foo", false},
		{"a find of files rooted in a subfolder", "find internal -name '*.go' -type f | xargs wc -l", false},
		{"a find of files rooted in a read-only tree", "find /work/lane-1.reads/api -type f | xargs wc -l", false},
		{"an fd of files", "fd -t f | xargs wc -l", false},
		{"an fd of files with a long option", "fd --type f go | xargs wc -l", false},
		{"a null separated listing", "find . -type f -print0 | xargs -0 wc -l", false},
		{"a loop over a glob", "for f in *.go; do head -5 \"$f\"; done", false},
		{"a loop over words", "for f in main.go go.mod; do cat \"$f\"; done", false},
		{"a loop over a glob in a folder", "for f in internal/*.go; do wc -l $f; done", false},
		{"a variable with a static word", "F=go.mod; cat $F", false},
		{"a variable with a static word in a path", "D=internal; ls $D/lanes", false},
		{"a variable in braces", "F=go.mod; cat \"${F}\"", false},

		{"a find that follows links", "find -L . -type f | xargs cat", true},
		{"a find that follows links by name", "find . -type f -follow | xargs cat", true},
		{"a find of every entry, links included", "find . -name '*.go' | xargs grep -l foo", true},
		{"a find rooted outside", "find /etc -type f | xargs cat", true},
		{"an fd that follows links", "fd -L -t f | xargs cat", true},
		{"an fd of every entry", "fd go | xargs cat", true},
		{"an fd rooted outside", "fd -t f . /etc | xargs cat", true},
		{"an rg that follows links", "rg -L -l X | xargs cat", true},
		{"an rg that follows links by name", "rg --follow --files | xargs cat", true},
		{"an rg rooted outside", "rg -l X /var | xargs cat", true},
		{"an rg listing no files", "rg X | xargs cat", true},
		{"an argument file", "git ls-files | xargs -a /etc/list wc -l", true},
		{"an argument file by name", "rg -l X | xargs --arg-file=list cat", true},
		{"a reader that names a file outside", "git ls-files | xargs cat /etc/passwd", true},
		{"a reader with a variable operand", "git ls-files | xargs cat $F", true},
		{"a git listing in another repository", "git -C /other ls-files | xargs cat", true},
		{"a git listing in another git directory", "git --git-dir=/other/.git ls-files | xargs cat", true},
		{"a git command that lists nothing", "git log --name-only | xargs cat", true},
		{"a listing that is no listing", "echo /etc/passwd | xargs cat", true},
		{"a listing two commands back", "git ls-files | sort | xargs cat", true},
		{"no listing at all", "xargs cat < files", true},
		{"a loop over words outside", "for f in /etc/passwd /etc/hosts; do cat \"$f\"; done", true},
		{"a loop with one word outside", "for f in main.go /etc/hosts; do head \"$f\"; done", true},
		{"a loop over a glob outside", "for f in /etc/*.conf; do cat \"$f\"; done", true},
		{"a loop over words a shell makes", "for f in $(ls /etc); do cat \"$f\"; done", true},
		{"a loop whose variable is another's", "for f in a b; do cat \"$g\"; done", true},
		{"a variable a substitution assigns", "F=$(echo x); cat $F", true},
		{"a variable a variable assigns", "F=$G; cat $F", true},
		{"a variable assigned a path outside", "F=/etc/passwd; cat $F", true},
		{"a variable assigned later", "cat $F; F=go.mod", true},
		{"a variable assigned in an earlier call", "cat $F", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if reason, asks := inside.Asks(ToolCall{Tool: "Bash", Command: tt.command, Cwd: "/work/lane-1"}); asks != tt.asks {
				t.Errorf("%q: asks = %v (%q), want %v", tt.command, asks, reason, tt.asks)
			}
		})
	}
	// A worktree with a link that leads out may hold it in a git listing, so no git listing is taken as inside.
	for _, command := range []string{"git ls-files | xargs wc -l", "git diff --name-only | xargs head", "git grep -l TODO | xargs sed -n 1p"} {
		if _, asks := linked.Asks(ToolCall{Tool: "Bash", Command: command, Cwd: "/work/lane-1"}); !asks {
			t.Errorf("%q asks nothing in a worktree with a link that leads out", command)
		}
	}
	if _, asks := linked.Asks(ToolCall{Tool: "Bash", Command: "rg --files | xargs wc -l", Cwd: "/work/lane-1"}); asks {
		t.Error("a listing that skips links asks in a worktree with a link that leads out")
	}
}
