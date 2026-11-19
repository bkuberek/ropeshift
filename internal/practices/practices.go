// Package practices is the practices check as data and rules. It holds the practice table, the package manager
// table, the finding ID grammar, and the evaluation that turns what a checkout holds into a Result. It reads no
// file, runs no git command, and calls no forge: an adapter gathers the Input, and Evaluate decides.
package practices

import "strings"

// TableVersion is the version of the practice table. It rises with any change to a row's ID, severity,
// applicability, or met rule.
const TableVersion = 1

// ID names one practice.
type ID string

// The practices, in the order of Table.
const (
	EnvIgnored        ID = "env-ignored"
	Secrets           ID = "secrets"
	CI                ID = "ci"
	PreCommit         ID = "pre-commit"
	Lockfile          ID = "lockfile"
	DependencyUpdates ID = "dependency-updates"
	SecurityPolicy    ID = "security-policy"
	Codeowners        ID = "codeowners"
	License           ID = "license"
	Tests             ID = "tests"
	BranchProtection  ID = "branch-protection"
	RequiredReview    ID = "required-review"
)

// Severity is how much a finding matters.
type Severity string

// The severities, from the most to the least serious.
const (
	High   Severity = "high"
	Medium Severity = "medium"
	Low    Severity = "low"
)

// rank orders severities, the most serious first. An unknown severity sorts last.
func (s Severity) rank() int {
	switch s {
	case High:
		return 0
	case Medium:
		return 1
	case Low:
		return 2
	}
	return 3
}

// Status is what one practice came to in a result.
type Status string

// The statuses of a practice.
const (
	// StatusMet says the practice holds.
	StatusMet Status = "met"
	// StatusFinding says the practice has at least one finding.
	StatusFinding Status = "finding"
	// StatusUnknown says the check could not read what the practice needs, and the reason says why.
	StatusUnknown Status = "unknown"
	// StatusNotApplicable says the practice does not apply to the repository, and the reason says why.
	StatusNotApplicable Status = "not_applicable"
)

// Practice is one row of the practice table.
type Practice struct {
	ID       ID       `json:"id"`
	Severity Severity `json:"severity"`
	// Applies says when the practice applies.
	Applies string `json:"applies"`
	// Why says why the practice matters.
	Why string `json:"why"`
	// Fix says how to meet the practice.
	Fix string `json:"fix"`
	// Template is the name of the template that supplies the missing file, empty for none.
	Template string `json:"template,omitempty"`
	// Since is the table version that added the practice.
	Since int `json:"since"`
}

// Command is the command that prints the practice's template, empty for a practice with none.
func (p Practice) Command() string {
	if p.Template == "" {
		return ""
	}
	return "ropeshift template show " + p.Template
}

var table = []Practice{
	{
		ID: EnvIgnored, Severity: High, Since: 1, Applies: "Always",
		Why:      "An env file holds credentials and local settings. Tracked, it reaches every clone and stays in the history for good; a lane environment's written files must be ignored too.",
		Fix:      "Add `.env`, `.env.*`, and `!.env.example` to `.gitignore`. For a tracked env file, run `git rm --cached <path>` and rotate every credential it held.",
		Template: "repository/gitignore",
	},
	{
		ID: Secrets, Severity: High, Since: 1, Applies: "Always",
		Why: "A credential in a tracked file or in history can be read by anyone who can read the repository, every lane included, and deleting the file does not remove it from history.",
		Fix: "Revoke and rotate the credential at its provider first, then remove it from the file. Removing it from history needs a history rewrite, which ropeshift does not do. Acknowledge a match that is a test fixture or an example.",
	},
	{
		ID: CI, Severity: Medium, Since: 1, Applies: "Always",
		Why:      "Checks on every change catch what a review misses, and with review target `pull_request` the base branch's required checks gate the merge.",
		Fix:      "Add a GitHub Actions workflow that runs the repository's checks and make its check required on the base branch (https://docs.github.com/en/actions/concepts/workflows-and-actions/workflows). With no CI, follow the no-CI recipe: a pre-commit hook and merge authority `person_merges` or `person_approves`.",
		Template: "pre-commit-config.yaml",
	},
	{
		ID: PreCommit, Severity: Low, Since: 1, Applies: "Always",
		Why:      "A quick check before every commit, each lane's commit included, catches errors before they reach a review.",
		Fix:      "Commit the pre-commit configuration, or in a workspace keep the `Makefile`'s `hooks` target and run `make hooks`.",
		Template: "pre-commit-config.yaml",
	},
	{
		ID: Lockfile, Severity: Low, Since: 1, Applies: "A package manager is detected and its manager has a lockfile",
		Why: "Without a lockfile each install can resolve different versions, so a build cannot be repeated and a compromised release can enter unseen.",
		Fix: "Run the manager's lock command and commit the file it writes.",
	},
	{
		ID: DependencyUpdates, Severity: Low, Since: 1, Applies: "A package manager is detected",
		Why:      "A dependency with a known vulnerability stays until someone updates it; an update configuration proposes each update as a pull request.",
		Fix:      "Copy the template to `.github/dependabot.yml` with one entry per package manager.",
		Template: "dependabot.yml",
	},
	{
		ID: SecurityPolicy, Severity: Low, Since: 1, Applies: "Always",
		Why:      "It tells a person who finds a vulnerability how to report it privately instead of in a public issue.",
		Fix:      "Commit `SECURITY.md` from the template and name a private reporting channel.",
		Template: "security.md",
	},
	{
		ID: Codeowners, Severity: Low, Since: 1, Applies: "Always",
		Why:      "It names who reviews each path; with a rule that requires code owner review, a change to a sensitive path needs its owner.",
		Fix:      "Commit `CODEOWNERS` from the template and name the owners.",
		Template: "codeowners",
	},
	{
		ID: License, Severity: Low, Since: 1, Applies: "The forge reports the repository public",
		Why: "A public repository with no license grants nobody the right to use, change, or share it.",
		Fix: "Choose a license and commit it as `LICENSE` (https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/licensing-a-repository).",
	},
	{
		ID: Tests, Severity: Low, Since: 1, Applies: "A package manager is detected",
		Why: "Tests are the evidence review and acceptance rest on; with none, a change can be judged only by reading it.",
		Fix: "Add tests by the language's convention.",
	},
	{
		ID: BranchProtection, Severity: Medium, Since: 1, Applies: "The repository has a forge binding",
		Why: "An unprotected base branch takes direct pushes, force pushes, and deletion, which bypass review.",
		Fix: "Protect the base branch with branch protection or a ruleset (https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches, https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets).",
	},
	{
		ID: RequiredReview, Severity: Medium, Since: 1, Applies: "The repository has a forge binding",
		Why: "Without a required approving review, a change can merge with nobody but its author having read it.",
		Fix: "Require at least one approving review on the base branch, by branch protection or a ruleset.",
	},
}

// Table returns the twelve practices in their fixed order.
func Table() []Practice {
	out := make([]Practice, len(table))
	copy(out, table)
	return out
}

// Lookup returns the practice with an ID.
func Lookup(id ID) (Practice, bool) {
	for _, p := range table {
		if p.ID == id {
			return p, true
		}
	}
	return Practice{}, false
}

// order is the position of a practice in the table, and the table's length for an unknown one.
func order(id ID) int {
	for i, p := range table {
		if p.ID == id {
			return i
		}
	}
	return len(table)
}

// joinPath joins a folder and a name, where the folder is empty for the root.
func joinPath(folder, name string) string {
	if folder == "" {
		return name
	}
	return folder + "/" + name
}

// splitPath returns a path's folder, empty for the root, and its name.
func splitPath(path string) (folder, name string) {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return "", path
	}
	return path[:i], path[i+1:]
}
