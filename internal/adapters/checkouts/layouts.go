package checkouts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/ports"
)

func (f *File) git(dir string) gitcli.Git { return gitcli.Git{Dir: dir, Env: f.Env} }

// source reads a clone's committed files at its default branch.
func (f *File) source(clone string) *repofiles.Source {
	return repofiles.New(repofiles.Config{Repository: clone, Env: f.Env})
}

// Ignored lists the directories a clone's ignore rules leave out of its working tree. Git collapses a directory
// that is ignored whole into one entry, so a directory inside one is not listed.
func (f *File) Ignored(ctx context.Context, clone string) ([]string, error) {
	out, err := f.git(clone).Output(ctx, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	var dirs []string
	for entry := range strings.SplitSeq(string(out), "\x00") {
		if dir, isDir := strings.CutSuffix(entry, "/"); isDir && dir != "" {
			dirs = append(dirs, dir)
		}
	}
	slices.Sort(dirs)
	return dirs, nil
}

// Plan reads every Markdown file the clone commits at its default branch's tip, and counts the ones the
// repository file format reads with a kind it knows.
func (f *File) Plan(ctx context.Context, clone string) (ports.PlanContent, error) {
	source := f.source(clone)
	tip, err := source.Head(ctx)
	if err != nil {
		return ports.PlanContent{}, err
	}
	blobs, err := source.Tree(ctx, tip)
	if err != nil || len(blobs) == 0 {
		return ports.PlanContent{}, err
	}
	hashes := make([]string, 0, len(blobs))
	for _, blob := range blobs {
		hashes = append(hashes, blob.Hash)
	}
	files, err := f.blobs(ctx, clone, hashes)
	if err != nil {
		return ports.PlanContent{}, err
	}
	var content ports.PlanContent
	for _, raw := range files {
		doc, err := repofiles.ParseFile(raw)
		if err != nil {
			continue
		}
		content.Files++
		content.Manifest = content.Manifest || doc.Repositories != nil
	}
	return content, nil
}

// blobs reads the content of each blob, in order, with one git process.
func (f *File) blobs(ctx context.Context, clone string, hashes []string) ([][]byte, error) {
	out, err := f.git(clone).Feed(ctx, []byte(strings.Join(hashes, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	files := make([][]byte, 0, len(hashes))
	for _, hash := range hashes {
		header, rest, ok := bytes.Cut(out, []byte("\n"))
		fields := strings.Fields(string(header))
		if !ok || len(fields) != 3 || fields[1] != "blob" {
			return nil, fmt.Errorf("git cat-file does not hold blob %s", hash)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 || len(rest) < size+1 {
			return nil, errors.New("git cat-file answered a blob it did not finish")
		}
		files = append(files, rest[:size])
		out = rest[size+1:]
	}
	return files, nil
}

// Documents counts the Markdown files the clone commits at its default branch's tip.
func (f *File) Documents(ctx context.Context, clone string) (int, error) {
	source := f.source(clone)
	tip, err := source.Head(ctx)
	if err != nil {
		return 0, err
	}
	blobs, err := source.Tree(ctx, tip)
	return len(blobs), err
}

// Tip is the commit the clone's default branch stands at.
func (f *File) Tip(ctx context.Context, clone string) (string, error) {
	return f.source(clone).Head(ctx)
}

// Tracked lists every path a commit of the clone holds.
func (f *File) Tracked(ctx context.Context, clone, commit string) ([]string, error) {
	out, err := f.git(clone).Output(ctx, "ls-tree", "-r", "-z", "--name-only", "--full-tree", commit)
	if err != nil {
		return nil, err
	}
	var tracked []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" {
			tracked = append(tracked, name)
		}
	}
	slices.Sort(tracked)
	return tracked, nil
}

// tracked lists the paths of the clone's default branch tip.
func (f *File) tracked(ctx context.Context, clone string) ([]string, error) {
	tip, err := f.Tip(ctx, clone)
	if err != nil {
		return nil, err
	}
	return f.Tracked(ctx, clone, tip)
}

// CodeTrees lists the code trees of the clone's default branch tip.
func (f *File) CodeTrees(ctx context.Context, clone string) ([]ports.CodeTree, error) {
	tracked, err := f.tracked(ctx, clone)
	return ports.CodeTreesOf(tracked), err
}

// Instructions says the clone's default branch tip holds an instruction file at its top.
func (f *File) Instructions(ctx context.Context, clone string) (bool, error) {
	tracked, err := f.tracked(ctx, clone)
	return ports.HoldsInstructions(tracked), err
}

var _ ports.Layouts = (*File)(nil)
