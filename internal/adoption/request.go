// Package adoption reads the layout around a clone and plans the adoption of it (FR-ADOPT-12): which clones are
// there, the shape of the layout, the files a restructuring adds, and the moves it leaves to the person. The
// control plane plans an adoption for a member, and a worker plans it again from the choices the work kept, so
// the code is shared and neither plane imports the other.
//
// The package is logic only. Every read of git or of the disk goes through the ports it is given, and the
// repository rules hold it to that: it imports no adapter, no process, and no file system.
package adoption

import (
	"fmt"
	"path"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
)

// Where a restructuring puts what it adds to the planning repository, and the folder of clones the moves it
// lists lead into.
const (
	// ManifestPath is where the manifest of the project's repositories goes in the planning repository.
	ManifestPath = planning.ManifestPath
	// CodeFolder is the folder of the planning repository that the moves lead into.
	CodeFolder = planning.CodeFolder
	// Program is the command the moves tell a person to run.
	Program = "ropeshift"
)

// MaxDepth is the most levels below a folder a plan searches for clones. A folder the clone ignores may hold a
// dependency tree or a build output, so a request cannot ask for a walk as deep as it likes.
const MaxDepth = planning.MaxLayoutDepth

// Request is what a member asks to be planned.
type Request struct {
	// Root is the top of the clone adopt runs in, as an absolute path on the worker's machine.
	Root string
	// Paths are the paths named beside it, absolute, each a clone or a folder that holds clones.
	Paths []string
	// Depth is how many levels below a folder are searched for clones, at most MaxDepth. Below 1 searches one.
	Depth int
	// Planning names the repository that holds the plan, by its clone's folder name.
	Planning string
	// Design names the design repository, by its clone's folder name.
	Design string
	// LeaveOut names the code trees of the design repository that a consolidation removes, relative to its top.
	LeaveOut []string
	// NewProject says the project does not exist yet, so no record is read.
	NewProject bool
	// Practices asks that each repository the plan would register be read for its practices, as a dry run lists
	// them (FR-PRAC-6). The plan's digest does not depend on them.
	Practices bool
}

// validate refuses a request that names no clone to run in, or a path that is not absolute.
func (in Request) validate() error {
	if in.Depth > MaxDepth {
		return fmt.Errorf("%w: --depth is at most %d, and %d was asked", ledger.ErrInvalidRepository, MaxDepth, in.Depth)
	}
	if !path.IsAbs(in.Root) {
		return fmt.Errorf("%w: the clone adopt runs in is not an absolute path: %q", ledger.ErrInvalidRepository, in.Root)
	}
	for _, named := range in.Paths {
		if !path.IsAbs(named) {
			return fmt.Errorf("%w: path %q is not absolute", ledger.ErrInvalidRepository, named)
		}
	}
	return nil
}
