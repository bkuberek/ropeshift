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
requirement the document states, under the ID the document already gives it, such as PAY-11, and with its text in one line. Keep
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
	if raw, err := planning.RenderInventory(inventoryExample); err == nil {
		out.WriteString("\nThe inventory is JSON in this shape, here for one repository:\n\n```json\n" + string(raw) + "```\n")
	}
	return out.String()
}
