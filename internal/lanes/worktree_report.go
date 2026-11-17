package lanes

// WorktreeReport is what the doctor says of where lane worktrees go: the root in force, the layer that set it,
// and each checkout the root puts a worktree inside a clone for.
type WorktreeReport struct {
	// Root is the root in force, as the profile spells it, with its {project} and {repo} tokens.
	Root string `json:"root"`
	// Source names the layer that set Root, as RootFromDefault and RootFromUser do.
	Source string `json:"source"`
	// Inside lists, for each checkout this worker keeps in any project, the clone the root resolves inside for
	// that checkout's project and folder. It is empty when no root falls inside a clone.
	Inside []RootInsideClone `json:"inside_clone,omitempty"`
	// Workspaces names, for each project that has a workspace on this worker, where it is and the root each of
	// the project's checkouts gets. A project with none is not listed.
	Workspaces []WorkspaceReport `json:"workspaces,omitempty"`
}

// WorkspaceReport is one project's workspace as the doctor says it: where it is, how the worker found it, whether
// lane worktrees may go in its .ropeshift/worktrees/ folder, and the root each checkout of the project gets.
type WorkspaceReport struct {
	Project string `json:"project"`
	// Path, Found, Clone, and Ignored are the Workspace the worker found.
	Path    string `json:"path"`
	Found   string `json:"found"`
	Clone   bool   `json:"clone"`
	Ignored bool   `json:"ignored"`
	// Line is the line the workspace's .gitignore lacks, when it is a clone that does not ignore the folder for
	// worktrees. Empty otherwise.
	Line string `json:"line,omitempty"`
	// Roots is the worktree root of each of the project's checkouts, as a new worktree would be placed.
	Roots []CheckoutRoot `json:"roots"`
}

// CheckoutRoot is the root a project's checkout gets for a new lane worktree.
type CheckoutRoot struct {
	Checkout string `json:"checkout"`
	Root     string `json:"root"`
}

// RootInsideClone is one checkout whose root lies inside a clone, so a lane there would fail with the reason
// worktree_inside_clone.
type RootInsideClone struct {
	// Project is the ID of the checkout's project.
	Project string `json:"project"`
	// Checkout is the path of this worker's checkout.
	Checkout string `json:"checkout"`
	// Root is the root as it resolves for Project and the folder of Checkout.
	Root string `json:"root"`
	// Clone is the clone the root is, or lies inside.
	Clone string `json:"clone"`
}

// RootFix says how to move a worktree root that lies inside a clone, for the layer that set it. A root the
// built-in layer chose lies under a worktree directory a person put inside a clone, so its fix moves that
// directory.
func RootFix(source string) string {
	if source == RootFromDefault {
		return "set ROPESHIFT_WORKTREE_DIR to a folder outside every clone"
	}
	return "set `[worktree] root` in your profile to a folder outside every clone, or remove it to use the built-in root"
}
