package fake

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/ports"
)

// ProjectFolders is a machine's folders held in memory. A test places the directories, links, and clones a
// worker finds, and reads what the worker wrote.
type ProjectFolders struct {
	mu sync.Mutex
	// dirs holds each directory, and files each file by path, so a directory is empty when no path lies under it.
	dirs  map[string]bool
	links map[string]string
	files map[string]string
	// tops are the tops of the clones placed or made, and commits the commit made at each.
	tops    map[string]bool
	commits map[string]string
	// staged are the paths each first commit was given to add, and initial the branch a first commit starts on.
	staged  map[string][]string
	initial string
	// CommitError is what a first commit fails with, as git does when no identity is set. The folder is still made
	// a repository, as `git init` does before the commit fails.
	CommitError error
	// calls lists what the worker did, in order: write and commit, each with its folder.
	calls []string
	// origins are the URL of each folder's origin, pushed the commit origin holds for each folder, and pushes the
	// pushes the worker ran.
	origins    map[string]string
	pushed     map[string]string
	pushes     []Push
	addErrors  map[string]error
	pushErrors map[string]error
	// branches are the branch each folder's HEAD names, "main" for a folder with a commit and no branch set, and
	// onRemote is called with the context of each Remote read.
	branches map[string]string
	onRemote func(ctx context.Context)
}

// NewProjectFolders returns a machine with only its root directory.
func NewProjectFolders() *ProjectFolders {
	return &ProjectFolders{
		dirs: map[string]bool{"/": true}, links: map[string]string{}, files: map[string]string{},
		tops: map[string]bool{}, commits: map[string]string{},
		origins: map[string]string{}, pushed: map[string]string{}, addErrors: map[string]error{}, pushErrors: map[string]error{},
		branches: map[string]string{},
	}
}

// Dir places a directory and the directories above it.
func (f *ProjectFolders) Dir(dir string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placeDir(dir)
}

func (f *ProjectFolders) placeDir(dir string) {
	for at := path.Clean(dir); ; at = path.Dir(at) {
		f.dirs[at] = true
		if at == "/" {
			return
		}
	}
}

// Link places a symbolic link at a path that leads to a target.
func (f *ProjectFolders) Link(at, target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links[path.Clean(at)] = path.Clean(target)
}

// Place puts a file in a folder, and the folders above it.
func (f *ProjectFolders) Place(file, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placeDir(path.Dir(file))
	f.files[path.Clean(file)] = text
}

// Clone makes a directory the top of a clone.
func (f *ProjectFolders) Clone(top string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placeDir(top)
	f.tops[path.Clean(top)] = true
}

// Text reads a file the worker or a test wrote.
func (f *ProjectFolders) Text(file string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	text, ok := f.files[path.Clean(file)]
	return text, ok
}

// Commit is the commit made at a folder, or empty.
func (f *ProjectFolders) Commit(dir string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commits[path.Clean(dir)]
}

// Inspected is what an inspection of a path reads now, for a test that looks at what a write made.
func (f *ProjectFolders) Inspected(at string) ports.FolderInspection {
	found, _ := f.Inspect(context.Background(), at)
	return found
}

// Done lists what the worker did.
func (f *ProjectFolders) Done() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *ProjectFolders) under(dir string) []string {
	var out []string
	for file := range f.files {
		if rel, ok := strings.CutPrefix(file, strings.TrimSuffix(dir, "/")+"/"); ok {
			out = append(out, rel)
		}
	}
	slices.Sort(out)
	return out
}

// real follows the links of the part of a path that exists.
func (f *ProjectFolders) real(at string) string {
	at = path.Clean(at)
	for range 8 {
		followed := false
		for link, target := range f.links {
			if at == link || strings.HasPrefix(at, link+"/") {
				at, followed = target+strings.TrimPrefix(at, link), true
				break
			}
		}
		if !followed {
			break
		}
	}
	return at
}

// Inspect reads what a test placed at a path.
func (f *ProjectFolders) Inspect(_ context.Context, at string) (ports.FolderInspection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	at = path.Clean(at)
	var found ports.FolderInspection
	_, link := f.links[at]
	found.Link = link
	found.Exists = link || f.dirs[at] || f.hasFile(at)
	found.Directory = f.dirs[at] && !link
	found.Empty = found.Directory && len(f.under(at)) == 0 && !f.hasChildDir(at)
	found.ParentDirectory = f.dirs[f.real(path.Dir(at))]
	found.Real = f.real(at)
	found.CloneTop = f.top(found.Real, found.Directory)
	return found, nil
}

func (f *ProjectFolders) hasFile(at string) bool {
	_, ok := f.files[at]
	return ok
}

func (f *ProjectFolders) hasChildDir(at string) bool {
	for dir := range f.dirs {
		if dir != at && strings.HasPrefix(dir, strings.TrimSuffix(at, "/")+"/") {
			return true
		}
	}
	return false
}

// top finds the clone that holds a path: the nearest top at or above a directory, or above an absent path.
func (f *ProjectFolders) top(at string, directory bool) string {
	start := path.Dir(at)
	if directory {
		start = at
	}
	for dir := start; ; dir = path.Dir(dir) {
		if f.tops[dir] {
			return dir
		}
		if dir == "/" {
			return ""
		}
	}
}

// Write makes the folder and its files, and leaves a file that exists.
func (f *ProjectFolders) Write(_ context.Context, folder string, files []ports.FolderFile, want ports.FolderID) (ports.FolderID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	folder = path.Clean(folder)
	if !f.dirs[path.Dir(folder)] {
		return "", fmt.Errorf("%s: %w", path.Dir(folder), ports.ErrNotFound)
	}
	// A folder is named by its path, which is all a memory disk says of it.
	id := ports.FolderID("fake:" + folder)
	if want != "" && want != id {
		return "", ports.ErrFolderChanged
	}
	f.dirs[folder] = true
	created := 0
	for _, file := range files {
		target := path.Join(folder, file.Path)
		if !strings.HasPrefix(target, folder+"/") {
			return "", errors.New("a file outside the folder")
		}
		f.placeDir(path.Dir(target))
		if _, exists := f.files[target]; !exists {
			f.files[target] = file.Text
			created++
		}
	}
	if created > 0 {
		f.calls = append(f.calls, fmt.Sprintf("write %s: %d files", folder, created))
	}
	return id, nil
}

// DefaultBranch is the branch a first commit starts on: main, until a test names another.
func (f *ProjectFolders) DefaultBranch(context.Context, string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.initial != "" {
		return f.initial
	}
	return "main"
}

// SetDefaultBranch names the branch a first commit starts on, in place of main.
func (f *ProjectFolders) SetDefaultBranch(branch string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initial = branch
}

// GitDirs lists every .git under a folder.
func (f *ProjectFolders) GitDirs(_ context.Context, folder string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := path.Clean(folder) + "/"
	var out []string
	add := func(at string) {
		if rel, found := strings.CutPrefix(at, prefix); found && (rel == ".git" || strings.HasSuffix(rel, "/.git")) {
			out = append(out, rel)
		}
	}
	for at := range f.files {
		add(at)
	}
	for at := range f.dirs {
		add(at)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// Held says which of the paths the folder holds.
func (f *ProjectFolders) Held(_ context.Context, folder string, paths []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var held []string
	for _, file := range paths {
		at := path.Join(path.Clean(folder), file)
		_, isFile := f.files[at]
		_, isLink := f.links[at]
		if isFile || isLink || f.dirs[at] {
			held = append(held, file)
		}
	}
	return held, nil
}

// Files lists the files under a folder.
func (f *ProjectFolders) Files(_ context.Context, folder string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.under(path.Clean(folder)), nil
}

// FirstCommit makes the folder a clone, and commits unless CommitError says it fails. A folder that is a clone already
// is the worker's own only on a resume.
func (f *ProjectFolders) FirstCommit(_ context.Context, folder, message string, resumed bool, named ports.FolderID, commit ports.CommitSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	folder = path.Clean(folder)
	if _, link := f.links[folder]; link || named != ports.FolderID("fake:"+folder) {
		return "", ports.ErrFolderChanged
	}
	if f.tops[folder] && !resumed {
		return "", errors.New("the folder holds a repository this worker did not make")
	}
	if id, ok := f.commits[folder]; ok {
		return id, nil
	}
	if len(commit.Paths) == 0 {
		return "", errors.New("no file to commit: every file the shape writes was in the folder already or is ignored by git")
	}
	f.tops[folder] = true
	if f.staged == nil {
		f.staged = map[string][]string{}
	}
	f.staged[folder] = slices.Clone(commit.Paths)
	f.calls = append(f.calls, "commit "+folder+": "+message)
	if f.CommitError != nil {
		return "", f.CommitError
	}
	f.commits[folder] = fmt.Sprintf("commit-%d", len(f.commits)+1)
	return f.commits[folder], nil
}

// Staged lists the paths the first commit at a folder was given to add, in order, or nothing when none was made.
func (f *ProjectFolders) Staged(folder string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.staged[path.Clean(folder)])
}

// Push is one push the worker ran.
type Push struct {
	Dir              string
	Branch           string
	CredentialHelper string
}

// Origin is the URL a test or the worker gave a folder's origin, or empty.
func (f *ProjectFolders) Origin(dir string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.origins[path.Clean(dir)]
}

// Pushes lists the pushes the worker ran, in order.
func (f *ProjectFolders) Pushes() []Push {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.pushes)
}

// FailAddRemote makes the next add of an origin at a folder fail with err, and FailPush the next push from it.
func (f *ProjectFolders) FailAddRemote(dir string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addErrors[path.Clean(dir)] = err
}

// FailPush makes the push from a folder fail with err until a test clears it with nil.
func (f *ProjectFolders) FailPush(dir string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushErrors[path.Clean(dir)] = err
}

// AddRemote gives the folder an origin, unless a test made adding one fail.
func (f *ProjectFolders) AddRemote(_ context.Context, folder, url string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	folder = path.Clean(folder)
	if err := f.addErrors[folder]; err != nil {
		return err
	}
	if f.origins[folder] != "" {
		return errors.New("error: remote origin already exists")
	}
	f.origins[folder] = url
	f.calls = append(f.calls, "remote "+folder+": "+url)
	return nil
}

// Push records a push of the folder's commit to its origin, unless a test made it fail.
func (f *ProjectFolders) Push(_ context.Context, folder string, push ports.PushSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	folder = path.Clean(folder)
	f.pushes = append(f.pushes, Push{Dir: folder, Branch: push.Branch, CredentialHelper: push.CredentialHelper})
	if err := f.pushErrors[folder]; err != nil {
		return err
	}
	if f.origins[folder] == "" {
		return errors.New("fatal: 'origin' does not appear to be a git repository")
	}
	f.pushed[folder] = f.commits[folder]
	f.calls = append(f.calls, "push "+folder+": "+push.Branch)
	return nil
}

// SetBranch names the branch a folder's HEAD is on, in place of main.
func (f *ProjectFolders) SetBranch(folder, branch string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branches[path.Clean(folder)] = branch
}

// OnRemote calls fn with the context of each Remote read, so a test can see the limit a caller put on it.
func (f *ProjectFolders) OnRemote(fn func(ctx context.Context)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onRemote = fn
}

// Remote reads the folder's origin, the branch and commit it holds, and the commit origin holds.
func (f *ProjectFolders) Remote(ctx context.Context, folder string) (ports.RemoteState, error) {
	f.mu.Lock()
	hook := f.onRemote
	f.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	folder = path.Clean(folder)
	state := ports.RemoteState{Origin: f.origins[folder], Head: f.commits[folder], RemoteHead: f.pushed[folder]}
	if state.Head != "" {
		state.Branch = "main"
		if branch := f.branches[folder]; branch != "" {
			state.Branch = branch
		}
	}
	return state, nil
}

var _ ports.ProjectFolders = (*ProjectFolders)(nil)
