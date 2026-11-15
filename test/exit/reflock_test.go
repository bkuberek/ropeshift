package exit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// refLockScript stands in for `git`. Once armed, it stands in for a second fetch in a clone that holds the
// lock of the clone's remote-tracking ref of main. The first claim's fetch of the base branch there, the one
// with an explicit refspec, finds the lock held until git has given up on it, and every later run goes to the
// real git. Intake's own fetch of that clone does nothing from the arming to that claim, so the claim's fetch
// has a ref to move: that is the collision, which in the daemon takes two fetches that both see the remote move.
const refLockScript = `#!/bin/sh
real=%s
clone=%s
state=%s
if [ "$(pwd)" = "$clone" ] && [ -e "$state/armed" ] && [ ! -e "$state/fired" ]; then
	case "$*" in
	"fetch --quiet origin")
		exit 0 ;;
	"fetch --quiet --no-tags origin +refs/heads/main:refs/remotes/origin/main")
		: >"$state/fired"
		lock=$("$real" rev-parse --git-path refs/remotes/origin/main.lock)
		mkdir -p "$(dirname "$lock")"
		: >"$lock"
		"$real" "$@"
		code=$?
		rm -f "$lock"
		exit "$code" ;;
	esac
fi
exec "$real" "$@"
`

// aGitThatLosesTheRefLock puts the stand-in `git` first on the install's PATH, after the CLI double, and
// returns the function that arms it for the clone at path. It returns a second function that says whether the
// stand-in held the lock, so a test can tell that the collision happened.
func (in *install) aGitThatLosesTheRefLock(clone string) (arm func(), fired func() bool) {
	in.t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		in.t.Fatal(err)
	}
	dir, state := in.t.TempDir(), in.t.TempDir()
	script := fmt.Sprintf(refLockScript, shellQuote(real), shellQuote(clone), shellQuote(state))
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil {
		in.t.Fatal(err)
	}
	claude, rest, _ := strings.Cut(in.path, string(os.PathListSeparator))
	in.path = strings.Join([]string{claude, dir, rest}, string(os.PathListSeparator))
	arm = func() {
		in.t.Helper()
		if err := os.WriteFile(filepath.Join(state, "armed"), nil, 0o600); err != nil {
			in.t.Fatal(err)
		}
	}
	fired = func() bool {
		_, err := os.Stat(filepath.Join(state, "fired"))
		return err == nil
	}
	return arm, fired
}

// shellQuote quotes one word for a POSIX shell.
func shellQuote(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}
