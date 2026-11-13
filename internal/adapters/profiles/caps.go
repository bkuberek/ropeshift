package profiles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// The keys a member's cap edit writes, in the order a new table holds them.
const (
	keyMaxLanes          = "max_lanes"
	keyMaxLanesPerSignIn = "max_lanes_per_sign_in"
)

var (
	tableHeader = regexp.MustCompile(`^\s*\[\[?([^\[\]]+)\]\]?\s*(#.*)?$`)
	capKey      = regexp.MustCompile(`^\s*(max_lanes|max_lanes_per_sign_in)\s*=`)
	keyLine     = regexp.MustCompile(`^\s*[A-Za-z0-9_"'-]`)
)

// errCannotEdit says the layer names its caps in a form a line edit cannot change safely.
var errCannotEdit = errors.New("the layer sets its lane caps in a form only a person can edit; edit [admission] in it by hand")

// editing holds to one edit of a user's layer at a time in this process, so two writes cannot lose one.
var editing sync.Mutex

// laneCapsOf reads the caps a user's layer sets.
func laneCapsOf(text string) (lanes.LaneCapSettings, error) {
	profile, err := Resolve(text)
	if err != nil {
		return lanes.LaneCapSettings{}, err
	}
	return lanes.LaneCapSettings{MaxLanes: profile.Admission.MaxLanes, MaxLanesPerSignIn: profile.Admission.MaxLanesPerSignIn}, nil
}

// EditLaneCaps writes a member's caps into the [admission] table of a user's layer and leaves every other
// line as it was, comments included. A cap that follows the recommendation, and a sign-in cap of zero,
// leave no key, because no key means the same. The edit is read back, and a layer that does not parse, or
// that sets its caps where a line edit cannot reach, is refused.
func EditLaneCaps(text string, caps lanes.LaneCapSettings) (string, error) {
	if err := caps.Check(); err != nil {
		return "", err
	}
	if _, err := Resolve(text); err != nil {
		return "", err
	}
	want := map[string]string{}
	if caps.MaxLanes != nil {
		want[keyMaxLanes] = strconv.FormatInt(*caps.MaxLanes, 10)
	}
	if caps.MaxLanesPerSignIn > 0 {
		want[keyMaxLanesPerSignIn] = strconv.FormatInt(caps.MaxLanesPerSignIn, 10)
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start, end := -1, len(lines)
	for i, line := range lines {
		match := tableHeader.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if match == nil {
			continue
		}
		if start >= 0 {
			end = i
			break
		}
		if strings.TrimSpace(match[1]) == "admission" {
			start = i
		}
	}
	var out []string
	if start < 0 {
		out = append(out, lines...)
		if len(want) > 0 {
			if len(out) > 0 {
				if !strings.HasSuffix(out[len(out)-1], "\n") {
					out[len(out)-1] += "\n"
				}
				out = append(out, "\n")
			}
			out = append(out, "[admission]\n")
			out = append(out, newKeys(want)...)
		}
	} else {
		table := []string{}
		last := 0
		for _, line := range lines[start+1 : end] {
			if match := capKey.FindStringSubmatch(line); match != nil {
				if value, ok := want[match[1]]; ok {
					table = append(table, match[1]+" = "+value+"\n")
					delete(want, match[1])
					last = len(table)
				}
				continue
			}
			table = append(table, line)
			if keyLine.MatchString(line) {
				last = len(table)
			}
		}
		table = append(table[:last], append(newKeys(want), table[last:]...)...)
		out = append(append(append(out, lines[:start+1]...), table...), lines[end:]...)
	}
	edited := strings.Join(out, "")
	read, err := laneCapsOf(edited)
	if err != nil {
		return "", err
	}
	if !sameLaneCaps(read, caps) {
		return "", errCannotEdit
	}
	return edited, nil
}

// newKeys are the lines of the keys still to write, in a fixed order.
func newKeys(want map[string]string) []string {
	var out []string
	for _, key := range []string{keyMaxLanes, keyMaxLanesPerSignIn} {
		if value, ok := want[key]; ok {
			out = append(out, key+" = "+value+"\n")
		}
	}
	return out
}

func sameLaneCaps(a, b lanes.LaneCapSettings) bool {
	if a.MaxLanesPerSignIn != b.MaxLanesPerSignIn || (a.MaxLanes == nil) != (b.MaxLanes == nil) {
		return false
	}
	return a.MaxLanes == nil || *a.MaxLanes == *b.MaxLanes
}

// LaneCaps reads the caps the user's layer sets.
func (f Filesystem) LaneCaps(context.Context) (lanes.LaneCapSettings, error) {
	text, err := readLayer(f.User)
	if err != nil {
		return lanes.LaneCapSettings{}, err
	}
	return laneCapsOf(text)
}

// SetLaneCaps edits the user's layer and replaces the file in one rename, so a reader finds the layer as
// it was or as it is now, never half of it. A layer that is a symbolic link stays one: the edit replaces
// the file it names. A new layer is the user's alone to read.
func (f Filesystem) SetLaneCaps(_ context.Context, caps lanes.LaneCapSettings) error {
	if f.User == "" {
		return errors.New("this machine has no user's layer to write")
	}
	editing.Lock()
	defer editing.Unlock()
	text, err := readLayer(f.User)
	if err != nil {
		return err
	}
	edited, err := EditLaneCaps(text, caps)
	if err != nil {
		return err
	}
	path, err := filepath.EvalSymlinks(f.User)
	if errors.Is(err, os.ErrNotExist) {
		path, err = f.User, nil
	}
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return replaceFile(path, []byte(edited), mode)
}

// replaceFile writes a file beside path, renames it over path, and syncs the directory, so the rename
// outlives a crash.
func replaceFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".profile-*.toml")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replacing %s: %w", filepath.Base(path), err)
	}
	return syncDir(dir)
}

// syncDir makes a directory's entries durable. A filesystem that cannot sync a directory has nothing more
// to give, and the rename stands.
func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := handle.Sync()
	if err := handle.Close(); err != nil {
		return err
	}
	if errors.Is(syncErr, syscall.EINVAL) || errors.Is(syncErr, syscall.ENOTSUP) {
		return nil
	}
	return syncErr
}

var _ ports.UserProfile = Filesystem{}
