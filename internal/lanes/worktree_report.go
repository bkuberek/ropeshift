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
