package lanes

import (
	"strings"

	"ropeshift.com/ropeshift/internal/planning"
)

// inventoryExample is one repository's inventory as the worker writes it, so the brief shows the agent the
// shape of the file it reads before it reads it.
var inventoryExample = planning.Inventory{Repositories: []planning.RepositoryInventory{{
	Name: "api", Repository: "4b825dc642cb6eb9a060e54bf8d69288fbee4904", Remote: "git@example.com:acme/api.git",
	DefaultBranch: "main", DefaultTip: "9fceb02d0ae598e95dc970b74767f19372d61af8",
	BaseBranch: "origin/main", BaseCommit: "9fceb02d0ae598e95dc970b74767f19372d61af8",
	Documents: []planning.InventoryDocument{
		{Path: "README.md", Digest: planning.DigestGitBlob + "d670460b4b4aece5915caf5c68d12f560a9fe3e4", FrontMatter: planning.FrontMatterNone,
			Headings: []planning.Heading{{Level: 1, Text: "Payments API"}}},
		{Path: "docs/requirements.md", Digest: planning.DigestGitBlob + "5716ca5987cbf97d6bb54920bea6adde242d87e6", FrontMatter: planning.FrontMatterNone,
			Headings: []planning.Heading{{Level: 1, Text: "Payments requirements"}, {Level: 2, Text: "PAY-11: One client"}, {Level: 2, Text: "PAY-12: Retries"}}},
		{Path: "site/about.md", Digest: planning.DigestGitBlob + "8ab686eafeb1f44702738c8b0f24f2567c36da6d", FrontMatter: planning.FrontMatterTOML,
			Headings: []planning.Heading{{Level: 1, Text: "About"}}},
		{Path: "site/index.md", Digest: planning.DigestGitBlob + "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", FrontMatter: planning.FrontMatterYAML,
			Headings: []planning.Heading{{Level: 1, Text: "Welcome"}}},
	},
	Files: []planning.InventoryFile{
		{Path: "site/about.md", Problem: `unknown file kind: ""`},
	},
	Branches: []planning.InventoryBranch{
		{
			Name: "feature/checkout-retry", Ref: "refs/heads/feature/checkout-retry", Tip: "1f7a7a472abf3dd9643fd615f6da379c4acb3e3a",
			MergeBase: "6c2b5d1e0c3a4f8e9b7d1a2c3e4f5a6b7c8d9e0f", Ahead: 3, Behind: 5, LastCommit: "2026-10-02T14:03:11Z",
		},
		{
			Name: "origin/spike/old-checkout", Ref: "refs/remotes/origin/spike/old-checkout", Tip: "2e65efe2a145dda7ee51d1741299f848e5bf752e",
			MergeBase: "0d1d7fc32e5a947fbd92ee598033d85bfc445a50", Ahead: 1, Behind: 40, LastCommit: "2025-03-18T09:12:40Z",
		},
	},
}}}

// adoptRules are what an adopt session does beyond any planning session (ADR-0023), in the order it does
// them. Each names the record it writes, and the examples after them show each record.
const adoptRules = `
This planning session adopts a project that already exists. The code, its documents, and its branches hold work people have
done, and your job is to bring that work into the repository file format above as it is, and then to plan what remains. The person
is here to answer what only they know. Work through the steps below in order.

1. Read what the project holds. Before you started, the worker wrote an inventory of every repository of this project into
` + InventoryFile + `, beside your context pack. No model wrote it: it lists each repository's remote, its default branch and tip,
and the base branch and commit it was read at; every Markdown file with its path, digest, front matter format (none, toml, or
yaml), and headings; the files whose TOML front matter intake would read, under ropeshift_files, with whether it reads each one
cleanly; and every branch the base does not hold, with its tip, where it left the base, how many commits it is ahead and behind,
and the time of its last commit. Its omitted list says what it does not read yet. Read the inventory first, then the documents
and the code it points to. This repository is the one you write in; the others are the read-only checkouts named above.

2. Onboard, do not rewrite. An existing requirements document, spec, architecture note, or decision record becomes a document of
the format by gaining front matter: add the +++ block at the top of the file, and keep its prose exactly as it was below it, every
heading and line. Give it a kind, an id, a title, and plan. In a requirements document, add one [[requirement]] table for each
requirement the document states, under the ID the document already gives it, such as PAY-11, and with its text in one line. A
decision record gets kind = "decision", an id such as adr-0007, a title, and a status of proposed, accepted, superseded, or
rejected, taken from the record's own status line, and proposed when it states none; it needs no plan, and only an accepted
record answers a question. Keep
existing IDs: never renumber, reword, merge, or split a requirement, and never write a document again in your own words. Move a
file only when the format needs it in another place, and then only with git mv, so its history follows it; never copy a file and
delete the old one. A document the plan holds is listed in the plan's documents.

3. Ask about what belongs to someone else. A file whose front matter belongs to another tool, such as a site generator's YAML
between --- lines or TOML with no kind, is not yours to change: ask the person whether to leave it as it is, and leave it until they
answer. Ask the same of a document whose meaning is unclear, and of two documents that contradict each other. Do not guess. Ask in
the conversation while the person is attached; when your question tool is refused, ask with the ` + ToolAsk + ` tool. Write each answer the
person gives into the plan file as a draft decision, with the question, the answer, and why.

4. Mark what is done, with evidence. A requirement the code already meets gets status = "met" and evidence: the files and tests
that show it, each as <repository>:<path> with the repository's name from the inventory. Read the code before you claim it, and
leave a requirement open, which is the default, when you are not sure. A milestone or a contract the code already delivers is
written with existing = true and no work orders; mark an existing contract shared = true when new work builds on it. The person
approves each of these claims with the plan, and the approval lists every one with its evidence.

5. Plan what remains, as any plan does: milestones in priority order, shared contracts first, then the contracts that use them,
each with criteria that cite requirement IDs, and the work orders that do the work.

6. Continue in-flight work. For each branch in the inventory, ask the person whether to keep it. A branch they keep becomes a work
order with continues naming the branch as the person calls it, without a remote's prefix, such as feature/checkout-retry, in the
repository that holds it: its lane starts from the branch's tip on a branch of its own, and the branch itself never moves. Record
each branch they leave out in the plan's [[left_out]] with ref = "branch:<name>" and the reason they gave. Never check out, merge,
rebase, push, or delete a branch someone else works on.

7. Write nothing else. Commit only the files above, in your own worktree of this repository and on your own branch: no code, no
tests, no build or CI files, and nothing in any other repository. Never commit to the default branch, push, or move another
branch. The person reads your branch as one change when they approve the plan, grouped into what exists, what continues, what
is new, and what is left out, with every file you added, changed, or moved, and nothing reaches the default branch until they
decide.
`

// Restructured is what the worker did to the repository an adopt lane plans in before the lane started
// (FR-ADOPT-14): the commit it made on the lane's branch, and the moves it leaves to the person.
type Restructured struct {
	// Commit is the commit that holds the workspace files and Subject its subject, and both are empty when the
	// repository held every file already, so there was nothing to commit.
	Commit  string
	Subject string
	// Files are the paths the commit holds.
	Files []string
	// Manifest is the reference the plan lists the manifest by in its documents, such as repositories:shop, and it
	// is empty when the commit holds no manifest.
	Manifest string
	// Moves are the clones the person moves into the repository's folder of clones, each as the commands that do it.
	Moves []planning.Move
	// Consolidated is what the worker imported when it consolidated a design repository into this repository
	// (FR-ADOPT-13), and nil when it imported nothing. Commit is then the merge commit.
	Consolidated *Consolidated
}

// Consolidated is the design repository's history, which the worker merged into the repository an adopt lane
// plans in, and what that merge settled and left for the session.
type Consolidated struct {
	// Design is the design repository's clone folder name.
	Design string
	// SecondParent is the design repository's tip, the merge commit's second parent, which holds the design
	// repository's version of every file.
	SecondParent string
	// Collisions are the paths both repositories held, each with the version the merge kept.
	Collisions []planning.Collision
	// LeftOut are the code trees the merge commit removed, which stay readable at the second parent.
	LeftOut []string
	// Instructions are the design repository's instruction files at the second parent, as paths.
	Instructions []string
}

// restructuredNotice tells an adopt lane what the worker restructured, so it neither writes those files again nor
// plans without them, and what stays the person's: the moves, which ropeshift never runs.
func restructuredNotice(done Restructured) string {
	if done.Consolidated != nil {
		return consolidatedNotice(done)
	}
	var out strings.Builder
	out.WriteString("\nBefore you started, the worker restructured this repository.")
	if done.Commit == "" {
		out.WriteString(" It found every workspace file in the repository already, so it made no commit.\n")
	} else {
		out.WriteString(" Commit " + done.Commit + " on your branch, \"" + done.Subject + "\", holds these files:\n")
		for _, file := range done.Files {
			out.WriteString("- " + file + "\n")
		}
		out.WriteString("They are part of your branch as they are: do not write them again, and do not undo them.\n")
	}
	out.WriteString(manifestSentence(done.Manifest))
	if len(done.Moves) != 0 {
		out.WriteString("The plan also lists moves, each a command for the person to run, because ropeshift never moves a clone. Tell the person " +
			"about them in the conversation, and never run them yourself:\n")
		for _, move := range done.Moves {
			for _, command := range move.Commands {
				out.WriteString("- " + command + "\n")
			}
		}
	}
	return out.String()
}

// manifestSentence tells the lane how the plan lists the manifest the worker wrote, by the reference given, or says
// nothing when the worker wrote none.
func manifestSentence(manifest string) string {
	if manifest == "" {
		return ""
	}
	return "The manifest, " + planning.ManifestPath + ", is a document of kind repositories. List it in the plan's documents as `" +
		manifest + "`, so the plan holds it and approving the plan registers the repositories it names.\n"
}

// consolidatedNotice tells an adopt lane what the worker imported when it consolidated the design repository: the
// merge commit and its second parent, so the session reads the design repository's own version of any file, the
// paths both repositories held and the trees left out, and the design repository's instruction files, which the
// session merges into the workspace's own (FR-ADOPT-13).
func consolidatedNotice(done Restructured) string {
	c := done.Consolidated
	var out strings.Builder
	out.WriteString("\nBefore you started, the worker consolidated the design repository, " + c.Design + ", into this repository. Commit " + done.Commit +
		" on your branch, \"" + done.Subject + "\", is a merge commit, and its second parent, " + c.SecondParent + ", is the design repository's tip. " +
		"Every design commit keeps its ID, and git log and git blame read through the merge. The merge is part of your branch as it is: " +
		"do not undo it, rebase it, or squash it, because that would drop the design history.\n")
	if len(c.Collisions) != 0 {
		out.WriteString("Both repositories held these paths. The merge kept this repository's version of each, and the design repository's version is at the " +
			"second parent, which `git show " + c.SecondParent + ":<path>` reads:\n")
		for _, collision := range c.Collisions {
			if collision.Kept == planning.KeptUnion {
				out.WriteString("- " + collision.Path + ", the lines of both files, this repository's first\n")
				continue
			}
			out.WriteString("- " + collision.Path + ", this repository's version\n")
		}
	}
	if len(c.LeftOut) != 0 {
		out.WriteString("These code trees were left out of the merge: the merge commit removed them, and they stay readable at the second parent and in the " +
			"old repository. Moving one into a repository of its own is the person's step, so leave it to them.\n")
		for _, tree := range c.LeftOut {
			out.WriteString("- " + tree + "\n")
		}
	}
	out.WriteString(manifestSentence(done.Manifest))
	if len(c.Instructions) != 0 {
		out.WriteString("The design repository's agent instructions are at the second parent:\n")
		for _, file := range c.Instructions {
			out.WriteString("- " + file + ", which `git show " + c.SecondParent + ":" + file + "` reads\n")
		}
		out.WriteString("Merge them into this repository's own instruction files as one commit on your branch, keeping both sets of rules: " +
			"write each rule of the design repository that this repository's files lack, and leave every rule they hold. " +
			"Where two rules contradict each other, ask the person about each contradiction, and never choose between them yourself.\n")
	}
	return out.String()
}

// adoptNotice tells a planning lane that adopts a project what adoption asks of it beyond any plan, with an
// example of every record it writes for work that already exists, a document before and after onboarding,
// and the shape of the inventory it reads.
func adoptNotice() string {
	var out strings.Builder
	out.WriteString(adoptRules)
	out.WriteString("\nThese are the records a plan for existing work adds to the format, one example of each, for the same project as " +
		"the examples above.\n")
	for _, example := range planning.AdoptionExamples {
		out.WriteString("\n" + example.Intro + "\n\n```markdown\n" + example.Text + "```\n")
	}
	out.WriteString("\nThis is a requirements document as the team wrote it:\n\n```markdown\n" + planning.OnboardedExample.Before + "```\n" +
		"\nand the same document onboarded: front matter on top, a table for each requirement under the ID it already had, one of " +
		"them met with its evidence, and the prose below exactly as it was:\n\n```markdown\n" + planning.OnboardedExample.After + "```\n")
	out.WriteString("\nThis is a decision record as the team wrote it:\n\n```markdown\n" + planning.OnboardedDecisionExample.Before + "```\n" +
		"\nand the same record onboarded: front matter of kind decision on top, the status its own line gives, and the prose below " +
		"exactly as it was:\n\n```markdown\n" + planning.OnboardedDecisionExample.After + "```\n")
	if raw, err := planning.RenderInventory(inventoryExample); err == nil {
		out.WriteString("\nThe inventory is JSON in this shape, here for one repository:\n\n```json\n" + string(raw) + "```\n")
	}
	return out.String()
}
