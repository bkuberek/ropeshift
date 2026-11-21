package claudecode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
)

// A directory a lane saves under a skills directory loads as a plugin when it holds a manifest: a plugin is
// `<name>@skills-dir` when "You saved a plugin directory that has a `.claude-plugin/plugin.json` under
// `~/.claude/skills/` or the project's `.claude/skills/`" ([plugin loading]), and a plugin brings its hooks and MCP
// servers. So the hold reads each directory under a skills directory it scans (the root's and each nested one) that
// holds a `.claude-plugin` directory, and compares every file under it with the same path at the lane's base: a plugin
// the base holds none of, and one whose files the lane added or changed, hold the lane, as a hook it adds does. A file
// it removed runs less, and holds nothing.
//
// [plugin loading]: https://code.claude.com/docs/en/plugins/loading#find-where-a-plugin-came-from
const pluginManifestDir = ".claude-plugin"

// keyPlugins is what a hold names for a plugin under a skills directory.
const keyPlugins = "plugins"

// heldPlugin is one plugin directory of the worktree whose files differ from the repository's at the lane's base: its
// path, the files the lane added or changed, the content each holds, whether the base holds a manifest there, and
// whether the base's side could be read. An overflow stands for the files past the bound, which were not compared.
type heldPlugin struct {
	path     string
	changed  []string
	sums     []string
	based    bool
	unread   bool
	overflow bool
}

// worktreePlugins are the plugin directories under the skills directories given, each a directory that holds a
// `.claude-plugin` directory, read through links, each directory once.
func worktreePlugins(spec ports.SessionSpec, skillDirs []string) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(dir, at string, depth int)
	walk = func(dir, at string, depth int) {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || seen[real] || depth > maxPieceDepth {
			return
		}
		seen[real] = true
		if info, err := os.Stat(filepath.Join(dir, pluginManifestDir)); err == nil && info.IsDir() {
			out = append(out, at)
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				walk(path, at+"/"+entry.Name(), depth+1)
			}
		}
	}
	for _, dir := range skillDirs {
		walk(filepath.Join(spec.Dir, filepath.FromSlash(dir)), dir, 0)
	}
	slices.Sort(out)
	return out
}

// pluginFiles are the regular files under a plugin directory of the worktree, by their slash-separated paths below it,
// read through links up to the bounds a repository's pieces hold to, and whether the read stayed within them.
func pluginFiles(spec ports.SessionSpec, root string) (map[string][]byte, bool) {
	out := map[string][]byte{}
	within := true
	seen := map[string]bool{}
	var walk func(dir, at string, depth int)
	walk = func(dir, at string, depth int) {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || seen[real] || !within {
			return
		}
		seen[real] = true
		if depth > maxPieceDepth {
			within = false
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			path, name := filepath.Join(dir, entry.Name()), pathpkg.Join(at, entry.Name())
			info, err := os.Stat(path)
			switch {
			case err != nil:
			case info.IsDir():
				walk(path, name, depth+1)
			case info.Mode().IsRegular():
				if len(out) == maxRepositoryPieceFiles {
					within = false
					return
				}
				raw, whole := boundedFile(path)
				if !whole {
					within = false
				}
				out[name] = raw
			}
		}
	}
	walk(filepath.Join(spec.Dir, filepath.FromSlash(root)), "", 0)
	return out, within
}

// treeAt is the blob of each file the repository holds below a directory at the lane's base, by its path below it, read
// once per side, and whether it was read: a read that fails says false and is read again next time.
func (b *baseSide) treeAt(spec ports.SessionSpec, dir string) (map[string]string, bool) {
	b.mu.Lock()
	tree, read := b.trees[dir]
	b.mu.Unlock()
	if read || spec.SettingsUnbased {
		return tree, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	files, err := repositoryGit(spec).FilesUnder(ctx, spec.Base, dir, maxRepositoryPieceFiles)
	if err != nil {
		return nil, false
	}
	tree = map[string]string{}
	for _, file := range files {
		tree[file.Path] = file.Blob
	}
	b.mu.Lock()
	b.trees[dir] = tree
	b.mu.Unlock()
	return tree, true
}

// comparePlugins are the plugin directories of the worktree whose files differ from the repository's at the lane's
// base: one it holds none of, and one with a file the lane added or changed.
func comparePlugins(spec ports.SessionSpec, base *baseSide, roots []string) []heldPlugin {
	var out []heldPlugin
	for _, root := range roots {
		files, within := pluginFiles(spec, root)
		tree, known := base.treeAt(spec, root)
		held := heldPlugin{path: root, unread: !known, overflow: !within}
		for _, name := range slices.Sorted(maps.Keys(files)) {
			if name == pluginManifestDir+"/plugin.json" && tree[name] != "" {
				held.based = true
			}
			if id, ok := gitcli.BlobID(spec.Base, files[name]); ok && known && tree[name] != "" && id == tree[name] {
				continue
			}
			sum := sha256.Sum256(files[name])
			held.changed = append(held.changed, name)
			held.sums = append(held.sums, hex.EncodeToString(sum[:]))
		}
		if len(held.changed) != 0 || held.overflow || held.unread {
			out = append(out, held)
		}
	}
	return out
}

// summary is what the hold says of one plugin directory.
func (p heldPlugin) summary() string {
	switch {
	case p.unread:
		return p.path + ": a plugin whose files the repository's side could not be read for, held as a change"
	case p.overflow:
		return p.path + fmt.Sprintf(": a plugin of more than %d files, or of one past the bound, held as a change", maxRepositoryPieceFiles)
	case !p.based:
		return p.path + ": a plugin the lane added, which loads with its hooks and servers: " + strings.Join(p.changed, ", ")
	}
	return p.path + ": a plugin whose files the lane added or changed: " + strings.Join(p.changed, ", ")
}

// bound is what a hold's digest binds of one plugin directory: each file the lane added or changed, and its content.
func (p heldPlugin) bound() string {
	parts := []string{fmt.Sprintf("plugin\x00%t\x00%t", p.unread, p.overflow)}
	for at, name := range p.changed {
		parts = append(parts, name+"\x00"+p.sums[at])
	}
	return strings.Join(parts, "\x00")
}

// restorePlugins puts each plugin directory whose files the lane added or changed back as the commit the lane's work
// began from holds it: a plugin the base holds none of loses its `.claude-plugin` directory, so it no longer loads, and
// one the base holds has each file the lane changed put back and each it added removed. It writes only through
// directories of the worktree's own, and acts only on a side it could read.
func restorePlugins(spec ports.SessionSpec, skillDirs []string) error {
	base := newBaseSide()
	ctx, cancel := context.WithTimeout(context.Background(), repositoryReadWait)
	defer cancel()
	for _, held := range comparePlugins(spec, base, worktreePlugins(spec, skillDirs)) {
		if held.unread || held.overflow {
			return fmt.Errorf("the repository's plugin at %s could not be compared, so it is not restored", held.path)
		}
		tree, _ := base.treeAt(spec, held.path)
		if !held.based {
			manifest := held.path + "/" + pluginManifestDir
			if _, err := ownParents(spec.Dir, manifest+"/.", false); err != nil {
				return err
			}
			if err := os.RemoveAll(filepath.Join(spec.Dir, filepath.FromSlash(manifest))); err != nil {
				return err
			}
			continue
		}
		for _, name := range held.changed {
			path := held.path + "/" + name
			full := filepath.Join(spec.Dir, filepath.FromSlash(path))
			blob := tree[name]
			if blob == "" {
				present, err := ownParents(spec.Dir, path, false)
				if err != nil {
					return err
				}
				if present {
					if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
						return err
					}
				}
				continue
			}
			raw, err := repositoryGit(spec).BlobOf(ctx, blob, maxRepositoryFile)
			if err != nil {
				return err
			}
			if _, err := ownParents(spec.Dir, path, true); err != nil {
				return err
			}
			if err := replaceFile(full, raw); err != nil {
				return err
			}
		}
	}
	return nil
}
