package ports

import (
	"context"
	"errors"
)

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
	// slash separated. It answers with the folder it wrote, named by what the disk says of it, which the
	// worker keeps. When want names a folder, the one opened must be that one, and a folder that became
	// another between the check and the open, or since an earlier write, is ErrFolderChanged and nothing is
	// written.
	Write(ctx context.Context, path string, files []FolderFile, want FolderID) (FolderID, error)
	// Files lists every file under the folder, as paths relative to it, slash separated and sorted, leaving
	// out each git directory, the one at its top and those of the repositories inside it. A path with nothing at it holds none.
	Files(ctx context.Context, path string) ([]string, error)
	// FirstCommit makes the folder a repository on its default branch, adds the files commit names and no other, and
	// commits them with the message, and returns the commit's ID. The default branch is the one git's user
	// configuration names as init.defaultBranch, and main when it names none. It runs under no hook, template, or signing the user's git configuration names.
	// A step that fails leaves what the earlier steps made. A folder that already holds a .git is taken as the
	// worker's own only when resumed says the worker journaled its git init: a resume answers with the commit
	// the repository holds and runs nothing, or goes on from an init with no commit, and a .git the worker did
	// not make is an error. Just before it runs git, which finds the folder by its path, it reads the path again,
	// and a path that is not the folder Write named is ErrFolderChanged and runs nothing.
	FirstCommit(ctx context.Context, path, message string, resumed bool, folder FolderID, commit CommitSpec) (string, error)
	// AddRemote runs `git remote add origin <url>` in the repository at path, and answers with git's message
	// when it fails.
	AddRemote(ctx context.Context, path, url string) error
	// Push runs `git push --set-upstream origin <branch>` in the repository at path, with no prompt. A push
	// that names a credential helper runs it for that one command, as the helper of github.com.
	Push(ctx context.Context, path string, push PushSpec) error
	// Remote reads what a restart needs to tell whether a step already ran: the URL of origin, the branch HEAD
	// names, the commit it holds, and the commit origin holds for that branch.
	Remote(ctx context.Context, path string) (RemoteState, error)
}

// CommitSpec is what a first commit holds.
type CommitSpec struct {
	// Paths are the files the commit adds, relative to the folder and slash separated: the files this worker wrote, and
	// none that were in the folder before it.
	Paths []string
}

// PushSpec is one push of a new repository's first commit.
type PushSpec struct {
	// Branch is the branch HEAD names, which the push sets the upstream of.
	Branch string
	// CredentialHelper is the absolute path of a `gh` that serves git's credentials for github.com over https
	// for this one push, or empty to push with the person's own git credentials.
	CredentialHelper string
}

// RemoteState is what a repository says of its origin.
type RemoteState struct {
	// Origin is the URL of the remote named origin, or empty when there is none.
	Origin string
	// Branch is the branch HEAD names, and Head the commit it holds, or empty when there is none.
	Branch string
	Head   string
	// RemoteHead is the commit origin holds for Branch, or empty when origin holds none or cannot be asked.
	RemoteHead string
}

// FolderID names a folder by what the disk says of it, so a later step can tell it from another folder at the same
// path. It is opaque to the worker, which keeps it in its journal.
type FolderID string

// ErrFolderChanged says the folder at a path is not the one the worker wrote: it was swapped for a link or for another
// folder.
var ErrFolderChanged = errors.New("the folder is no longer the one this worker wrote")
