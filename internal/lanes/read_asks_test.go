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

// What an agent runs all day asks nothing when it reads inside, and only what a strict allowlist proves: a pipe into
// xargs from one listing of three kinds rooted inside, with xargs taking only options that split and batch, and a loop
// over static words. Everything else that a shell alone can resolve asks, and each case below that asks is a bypass the
// review found, kept as a regression test.
func TestOrdinaryReadsInsideAskNothingAndEveryBypassAsks(t *testing.T) {
	policy := CompilePolicy(PolicySpec{
		Worktree: "/work/lane-1", Home: "/home/person", AskReadRoots: []string{"/work/lane-1", "/work/lane-1.reads/api"},
	})
	allowed := []string{
		"xargs -0 echo", "xargs --max-args=1 -0 echo", "xargs --null --no-run-if-empty echo", "xargs --nul --verbose echo", "xargs -ifoo echo foo",
		"echo hi | xargs -0 echo", "echo a b | xargs -n 1 echo hi", "echo a | xargs -r -P 2 echo", "cat *.go", "cat internal/*.go", "head -5 main.go", "rg TODO internal", "ls internal", "find internal -name '*.go'", "wc -l main.go go.mod",
	}
	for _, command := range allowed {
		if reason, asks := policy.Asks(ToolCall{Tool: "Bash", Command: command, Cwd: "/work/lane-1"}); asks {
			t.Errorf("%q asks (%q)", command, reason)
		}
	}
	bypasses := map[string][]string{
		"a form the hook once allowed": {
			"rg -l X | xargs sed -n 1p",
			"rg --files | xargs wc -l",
			"rg -l X internal | xargs cat",
			"rg -l -e X | xargs cat",
			"rg -l -t go -g '!vendor' -i -F -w X | xargs wc -l",
			"rg -l --type go --glob '*.go' X | xargs wc -l",
			"rg --files -t go | xargs wc -l",
			"fd -t f | xargs wc -l",
			"fd --type f go | xargs wc -l",
			"fd -t f -e go -E vendor -H -I -d 3 | xargs wc -l",
			"fd -g -t f '*.go' internal | xargs wc -l",
			"find . -type f -name '*.go' | xargs grep -l foo",
			"find internal -name '*.go' -type f -maxdepth 2 | xargs wc -l",
			"find /work/lane-1.reads/api -type f | xargs wc -l",
			"find . -type f -print0 | xargs -0 wc -l",
			"find . ! -path './vendor/*' -type f -iname '*.go' -mindepth 1 -print | xargs wc -l",
			"find . -type f -not -name '*_test.go' | xargs wc -l",
			"rg -l X | xargs -0 -n 1 -P 4 -r wc -l",
			"rg -l X | xargs --null --max-args=1 --max-procs=2 --no-run-if-empty --max-lines=3 wc -l",
			"rg -l X | xargs -d '\\n' -L 1 cat",
			"for f in *.go; do head -5 \"$f\"; done",
			"for f in main.go go.mod; do cat \"$f\"; done",
			"for f in internal/*.go; do wc -l $f; done",
			"for f in main.go; do for g in go.mod; do cat \"$f\" \"$g\"; done; done",
			"for f in main.go; do cat \"${f}\"; done",
		},
		"an xargs that reads its arguments from a file": {
			"xargs -a /etc/passwd echo", "xargs -a/etc/passwd echo", "xargs -ra /etc/passwd echo", "xargs -0a /etc/passwd echo",
			"xargs --arg-file=/etc/passwd echo", "xargs --arg-file /etc/passwd echo", "xargs --arg /etc/passwd echo", "xargs --arg=/etc/passwd echo",
			"xargs --a /etc/passwd echo", "xargs --a=/etc/passwd echo", "xargs --ar /etc/passwd echo", "xargs --arg-f=/etc/passwd echo",
			"xargs -0n 1 -a /etc/passwd echo", "xargs -rd x -a /etc/passwd echo", "xargs -0n1 -a /etc/passwd echo", "xargs -rd , -a /etc/passwd echo",
			"xargs -0I {} -a /etc/passwd echo", "xargs -n 1 -a /etc/passwd echo", "xargs -i -a /etc/passwd echo", "xargs -e -a /etc/passwd echo", "xargs -l -a /etc/passwd echo",
			"xargs --max-args 1 -a /etc/passwd echo", "xargs --max-procs 2 -a /etc/passwd echo", "xargs --delimiter , -a /etc/passwd echo",
			"xargs --max-args 1 --arg-file /etc/passwd echo", "xargs --n -a /etc/passwd echo", "xargs --max-args 1 -0 echo",
			"xargs --unknown value -a /etc/passwd echo", "xargs --null --max-args 1 -a /etc/passwd echo", "xargs -a list echo", "echo hi | xargs -a /etc/passwd echo", "env xargs -a /etc/passwd echo",
		},
		"a bypass of the forms the hook once allowed": {
			// A directory named `a ` that holds etc/hostname splits, in xargs, into the words `a` and `etc/hostname`.
			"find . -type f -name hostname | xargs cat", "rg --files | xargs cat", "find . -not -type f | xargs cat", "find . -type f -not -type f | xargs cat",
			"rg --files | xargs -d / cat", "rg --files | xargs -d '/' wc -l", "g() { f=/etc/passwd; }; for f in go.mod; do g; cat $f; done",
			"g() { f=/etc/passwd; }; for f in go.mod; do g; cat \"$f\"; done", "for f in go.mod; do cat $f; done", "for f in go.mod; do cat \"$f\"; done",
		},
		"a variable the call assigned": {
			"F=go.mod; cat $F", "D=internal; ls $D/lanes", "F=go.mod; cat \"${F}\"", "F=$(echo x); cat $F", "F=$G; cat $F", "F=/etc/passwd; cat $F",
			"cat $F; F=go.mod", "cat $F", "export F=/etc/passwd; cat $F", "declare F=/etc/passwd; cat $F", "typeset F=/etc/passwd; cat $F",
			"printf -v F %s /etc/passwd; cat $F", "eval 'F=/etc/passwd'; cat $F", "F=x; F+=/etc/passwd; cat $F",
			"read F < /dev/null; cat $F", "[ -n \"$X\" ] && F=/etc/passwd; cat $F",
		},
		"a loop body that sets the variable": {
			"for f in a b; do f=/etc/passwd; cat $f; done", "for f in a; do export f=/etc/passwd; cat \"$f\"; done",
			"for f in a; do declare f=/etc/passwd; cat $f; done", "for f in a; do printf -v f %s /etc/passwd; cat $f; done",
			"for f in a; do eval f=/etc/passwd; cat $f; done", "for f in a; do read f < /dev/null; cat $f; done",
			"for f in a; do f+=/../../etc/passwd; cat \"$f\"; done", "for f in a; do [ -n \"$X\" ] && f=/etc/passwd; cat \"$f\"; done",
			"for f in a; do local f=/etc/passwd; cat $f; done", "for f in a; do ((f=1)); cat $f; done", "for f in a; do env f=/etc/passwd cat $f; done",
			"for f in a; do source ./set.sh; cat $f; done", "for f in a; do $SETTER; cat $f; done",
		},
		"a loop that reads outside": {
			"for f in /etc/passwd /etc/hosts; do cat \"$f\"; done", "for f in main.go /etc/hosts; do head \"$f\"; done",
			"for f in /etc/*.conf; do cat \"$f\"; done", "for f in $(ls /etc); do cat \"$f\"; done", "for f in a b; do cat \"$g\"; done",
			"for f in a; do cat \"$f\"; done; cat $f", "for f in $X; do cat $f; done", "for f in ~/x; do cat $f; done", "for f in a; do cat \"${f:-/etc/passwd}\"; done",
			"for ((i=0;i<1;i++)); do cat $i; done", "for f in ../../etc/passwd; do cat $f; done", "for f in a; do cat \"$f\"/../../../etc/passwd; done",
		},
		"a git listing": {
			"git ls-files | xargs wc -l", "git diff --name-only | xargs head -5", "git grep -l TODO | xargs sed -n 1p",
			"git ls-files --format='/etc/passwd' | xargs cat", "git diff --name-only --line-prefix=/etc/ | xargs cat",
			"git -C /other ls-files | xargs cat", "git --git-dir=/other/.git ls-files | xargs cat", "git log --name-only | xargs cat",
		},
		"an option xargs takes": {
			"rg -l X | xargs -I{} cat {}", "rg -l X | xargs -I {} cat {}", "rg -l X | xargs --replace cat {}", "rg -l X | xargs -i cat {}",
			"rg -l X | xargs -a /etc/list cat", "rg -l X | xargs -a list cat", "rg -l X | xargs --arg-file=list cat", "rg -l X | xargs --arg-f=list cat",
			"rg -l X | xargs --arg list cat", "rg -l X | xargs --arg-file list cat", "rg -l X | xargs -0r cat", "rg -l X | xargs -n cat",
			"rg -l X | xargs -n x cat", "rg -l X | xargs -P $N cat", "rg -l X | xargs --max-args 1 cat", "rg -l X | xargs --max-arg=1 cat",
			"rg -l X | xargs -p cat", "rg -l X | xargs -t cat", "rg -l X | xargs -x cat", "rg -l X | xargs -s 100 cat", "rg -l X | xargs -E eof cat",
			"rg -l X | xargs -d '/etc' cat", "rg -l X | xargs --delimiter= cat", "rg -l X | xargs -- cat", "rg -l X | xargs --show-limits cat",
			"rg -l X | xargs --process-slot-var=x cat",
		},
		"a source that is not the whole of the pipe": {
			"(echo /etc/passwd; rg -l X) | xargs cat", "(echo /etc/passwd; git ls-files) | xargs cat", "{ echo /etc/passwd; rg --files; } | xargs cat",
			"echo /etc/passwd; rg -l X | xargs cat", "rg -l X | sort | xargs cat", "rg -l X | tee out | xargs cat", "echo /etc/passwd | xargs cat",
			"cd internal && rg -l X | xargs cat", "rg -l X && echo /etc/passwd | xargs cat", "xargs cat < files", "rg -l X | xargs cat < list",
			"rg -l X | xargs cat <<< /etc/passwd", "rg -l X < list | xargs cat", "env rg -l X | xargs cat", "rg -l X | env xargs cat", "rg -l X | sudo xargs cat",
			"sh -c 'echo /etc/passwd; rg -l X' | xargs cat", "$(echo rg) -l X | xargs cat", "rg -l X $(echo /etc) | xargs cat",
		},
		"an rg listing that is not proved": {
			"rg -L -l X | xargs cat", "rg --follow --files | xargs cat", "rg -l --follow X | xargs cat", "rg --files-with-matches X | xargs cat",
			"rg -l --null X | xargs -0 cat", "rg -l -0 X | xargs -0 cat", "rg --files --null | xargs -0 cat", "rg -l --path-separator=/x X | xargs cat",
			"rg -l X /var | xargs cat", "rg --files /etc | xargs cat", "rg X | xargs cat", "rg -l | xargs cat", "rg --files -l | xargs cat",
			"rg --files -e X | xargs cat", "rg -il X | xargs cat", "rg -l -uu X | xargs cat", "rg -l X --no-ignore | xargs cat",
			"rg --files --sort path | xargs cat", "rg --files --hidden | xargs cat", "rg -l -g | xargs cat", "rg -l --replace=/etc X | xargs cat",
			"rg -l --pre /bin/cat X | xargs cat", "rg --files ~ | xargs cat", "rg --files ../.. | xargs cat", "rg --files $D | xargs cat",
		},
		"an fd listing that is not proved": {
			"fd -L -t f | xargs cat", "fd --follow -t f | xargs cat", "fd go | xargs cat", "fd -t d | xargs cat", "fd -t f -t f | xargs cat",
			"fd -t f -t d | xargs cat", "fd -t l | xargs cat", "fd -t f . /etc | xargs cat", "fd -t f go /etc | xargs cat", "fd -t f -x cat | xargs cat",
			"fd -t f -X cat | xargs cat", "fd -t f --exec cat | xargs cat", "fd -t f --exec-batch cat | xargs cat", "fd -t f --format /etc/passwd | xargs cat",
			"fd -t f -l | xargs cat", "fd -t f -0 | xargs -0 cat", "fd -t f -a | xargs cat", "fd -t f --search-path /etc | xargs cat",
			"fd -t f -u | xargs cat", "fd -t f -S +1k | xargs cat", "fd -t f ~ | xargs cat", "fd -t | xargs cat", "fd -t f -e | xargs cat",
		},
		"a find listing that is not proved": {
			"find -L . -type f | xargs cat", "find . -type f -follow | xargs cat", "find -H . -type f | xargs cat", "find . -name '*.go' | xargs grep -l foo",
			"find . -type d | xargs cat", "find . -type l | xargs cat", "find . -type f -type f | xargs cat", "find . -type f -xtype l | xargs cat",
			"find . -type f -o -name x | xargs cat", "find . -type f -or -name x | xargs cat", "find . -type f , -name x | xargs cat",
			"find . -type f -printf '%l\\n' | xargs cat", "find . -type f -fprint /tmp/x | xargs cat", "find . -type f -fprint0 /tmp/x | xargs cat",
			"find . -type f -ls | xargs cat", "find . -type f -exec cat {} ; | xargs cat", "find . -type f -execdir cat {} ; | xargs cat",
			"find . -type f -ok cat {} ; | xargs cat", "find . -type f -delete | xargs cat", "find /etc -type f | xargs cat", "find / -type f | xargs cat",
			"find . /etc -type f | xargs cat", "find ~ -type f | xargs cat", "find ../.. -type f | xargs cat", "find . -type f -print -name x | xargs cat",
			"find . -type f -print0 -print | xargs cat", "find . -type f ( -name a ) | xargs cat", "find . -type f -name | xargs cat",
			"find . -type f -maxdepth x | xargs cat", "find $D -type f | xargs cat", "find . -type f -newer /etc/passwd | xargs cat",
			"find . -type f -samefile /etc/passwd | xargs cat",
		},
		"a reader that reads outside": {
			"rg -l X | xargs cat /etc/passwd", "rg -l X | xargs cat $F", "rg -l X | xargs cp /etc/passwd", "rg -l X | xargs cp -t /etc",
			"rg -l X | xargs install /etc/hosts", "rg -l X | xargs rsync /etc/hosts", "rg -l X | xargs find", "rg -l X | xargs grep -f /etc/passwd",
			"rg -l X | xargs sed -f /etc/passwd", "rg -l X | xargs ln -s /etc", "rg -l X | xargs python3 -c 'open(\"/etc/passwd\")'",
			"rg -l X | xargs ls /etc", "rg -l X | xargs rg foo /etc", "rg -l X | xargs tar -cf - /etc",
		},
	}
	total := 0
	for group, commands := range bypasses {
		for _, command := range commands {
			total++
			if _, asks := policy.Asks(ToolCall{Tool: "Bash", Command: command, Cwd: "/work/lane-1"}); !asks {
				t.Errorf("%s: %q asks nothing", group, command)
			}
		}
	}
	t.Logf("%d bypass cases and %d allowed forms", total, len(allowed))
}
