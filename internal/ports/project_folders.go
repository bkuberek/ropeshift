package ports

import "context"

// FolderInspection is what a worker finds of the folder it is asked to write.
type FolderInspection struct {
	// Exists says something is at the path, a symbolic link included, which is not followed to say it.
	Exists bool
	// Link says the path is a symbolic link.
	Link bool
	// Directory says the path is a directory, and Empty that it holds nothing. A path that is no directory is
	// neither.
	Directory bool
	Empty     bool
	// ParentDirectory says the path's parent exists as a directory.
	ParentDirectory bool
	// Real is the path with every symbolic link followed, as far as the path exists: an absent folder under a
	// parent that is a link reads as the link's target and the folder's name.
	Real string
	// CloneTop is the top of the clone whose working tree holds the path, as git names it, or empty when
	// none does. A directory is read in itself, and a path that is absent from its parent.
	CloneTop string
}

// FolderFile is one file of a folder a worker writes.
type FolderFile struct {
	Path string
	Text string
}

// ProjectFolders is a worker's access to the folder a template write fills: what it finds there, the
// writing of a shape's files, and the folder's first commit.
type ProjectFolders interface {
	// Inspect reads what is at an absolute path and what holds it.
	Inspect(ctx context.Context, path string) (FolderInspection, error)
	// Write makes the folder when it is absent, never its parents, and then each file with the folders it
	// needs, every file opened only to be created. A file that exists is left as it is and is not an error,
	// so a write the worker repeats after a crash writes the rest. Paths are relative to the folder and
	// slash separated.
	Write(ctx context.Context, path string, files []FolderFile) error
	// Files lists every file under the folder, as paths relative to it, slash separated and sorted, leaving
	// out the git directory at its top. A path with nothing at it holds none.
	Files(ctx context.Context, path string) ([]string, error)
	// FirstCommit makes the folder a repository, adds every file, and commits them with the message, and
	// returns the commit's ID. It runs under no hook, template, or signing the user's git configuration names.
	// A step that fails leaves what the earlier steps made. A folder that already holds a .git is taken as the
	// worker's own only when resumed says the worker journaled its git init: a resume answers with the commit
	// the repository holds and runs nothing, or goes on from an init with no commit, and a .git the worker did
	// not make is an error.
	FirstCommit(ctx context.Context, path, message string, resumed bool) (string, error)
}
