package practices

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Visibility is a repository's visibility on its forge.
type Visibility string

// The visibilities a forge reports.
const (
	VisibilityPublic   Visibility = "public"
	VisibilityPrivate  Visibility = "private"
	VisibilityInternal Visibility = "internal"
)

// ForgeFacts is what the forge says of a repository and its base branch. A fact the forge could not give is
// left at its zero value, and its Unknown field holds the reason, never a finding (FR-PRAC-1).
type ForgeFacts struct {
	Visibility Visibility `json:"visibility,omitempty"`
	// VisibilityUnknown is why Visibility could not be read.
	VisibilityUnknown string `json:"visibility_unknown,omitempty"`
	// Protected says branch protection or a ruleset rule applies to the base branch.
	Protected bool `json:"protected,omitempty"`
	// ProtectedUnknown is why Protected could not be read.
	ProtectedUnknown string `json:"protected_unknown,omitempty"`
	// ReviewCount is the number of approving reviews a merge requires, by branch protection or a ruleset.
	ReviewCount int `json:"review_count,omitempty"`
	// ReviewUnknown is why ReviewCount could not be read. A reader that cannot settle the count leaves it set,
	// so a count of 0 always means the forge said no review is required.
	ReviewUnknown string `json:"review_unknown,omitempty"`
}

// SecretMatch is one match of a secret rule. It holds the digest of the matched text and never the text.
type SecretMatch struct {
	Rule   string `json:"rule"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Commit string `json:"commit"`
	// Digest is the hex SHA-256 of the matched text.
	Digest string `json:"digest"`
}

// Merge authorities the `ci` practice reads. They are the values of the repository record's merge authority.
const (
	mergeAuthorityPersonMerges   = "person_merges"
	mergeAuthorityPersonApproves = "person_approves"
)

// Snapshot is what a checkout holds at one commit, as the check reads it.
type Snapshot struct {
	// Commit is the commit read.
	Commit string
	// Paths are the tracked paths at the commit, with `/` separators and relative to the repository's root.
	Paths []string
	// Read returns a tracked file's text, and false for a file it cannot read.
	Read func(path string) (string, bool)
	// Ignored holds the ignore answer for each path of Probes.
	Ignored map[string]bool
}

// Input is everything Evaluate decides from.
type Input struct {
	Repository string
	Commit     string
	BaseBranch string
	Worker     string
	CheckedAt  time.Time
	// RulesVersion is the version of the secret rule set the scan used.
	RulesVersion int
	// ScannedFrom is the commit the secret scan started from, empty for a scan of the whole history.
	ScannedFrom string
	// Skipped counts the files the secret scan skipped.
	Skipped int

	// Paths, Read, and Ignored are the checkout's snapshot.
	Paths   []string
	Read    func(path string) (string, bool)
	Ignored map[string]bool

	// MergeAuthority is the repository's merge authority; empty counts as person_merges, the default of a
	// repository not yet registered.
	MergeAuthority string
	// Forge holds the forge's facts, and is nil for a repository with no forge binding.
	Forge *ForgeFacts
	// Secrets are the matches of the secret scan, and SecretsUnknown says why the scan could not run.
	Secrets        []SecretMatch
	SecretsUnknown string
}

// envProbeNames are the env files whose ignore answer the check asks for in each folder that holds a manifest.
var envProbeNames = []string{".env", ".env.local", ".env.development", ".env.production", ".env.test"}

// Probes returns the paths whose ignore answer Evaluate needs: each env probe name in the root and in each
// folder that holds a detected manifest.
func Probes(paths []string) []string {
	folders := map[string]bool{"": true}
	for _, p := range detect(paths) {
		folders[p.folder] = true
	}
	var out []string
	for folder := range folders {
		for _, name := range envProbeNames {
			out = append(out, joinPath(folder, name))
		}
	}
	slices.Sort(out)
	return out
}

// project is one folder's manifests of one package manager.
type project struct {
	manager   Manager
	folder    string
	manifests []string
}

// detect finds the package manager projects among tracked paths, skipping the folders SkippedSegments names. They
// are in the order of the manager table and then by folder.
func detect(paths []string) []project {
	found := map[string]map[string][]string{}
	for _, path := range paths {
		if IsSkipped(path) {
			continue
		}
		folder, name := splitPath(path)
		for _, m := range managers {
			if slices.Contains(m.Manifests, name) {
				if found[m.Name] == nil {
					found[m.Name] = map[string][]string{}
				}
				found[m.Name][folder] = append(found[m.Name][folder], name)
			}
		}
	}
	var out []project
	for _, m := range managers {
		var folders []string
		for folder := range found[m.Name] {
			folders = append(folders, folder)
		}
		slices.Sort(folders)
		for _, folder := range folders {
			names := found[m.Name][folder]
			slices.Sort(names)
			out = append(out, project{manager: m, folder: folder, manifests: names})
		}
	}
	return out
}

// outcome is what one practice came to, before it joins the result.
type outcome struct {
	status   Status
	reason   string
	findings []Finding
}

func metOutcome() outcome                 { return outcome{status: StatusMet} }
func unknownOutcome(why string) outcome   { return outcome{status: StatusUnknown, reason: why} }
func notApplicable(why string) outcome    { return outcome{status: StatusNotApplicable, reason: why} }
func findingOutcome(f ...Finding) outcome { return outcome{status: StatusFinding, findings: f} }

// combine joins the outcomes of a practice's parts: any finding makes it a finding, then any unknown makes it
// unknown, then any met makes it met, and otherwise it is not applicable.
func combine(parts []outcome, none outcome) outcome {
	if len(parts) == 0 {
		return none
	}
	var findings []Finding
	var unknown, inapplicable []string
	met := false
	for _, part := range parts {
		switch part.status {
		case StatusFinding:
			findings = append(findings, part.findings...)
		case StatusUnknown:
			unknown = appendOnce(unknown, part.reason)
		case StatusNotApplicable:
			inapplicable = appendOnce(inapplicable, part.reason)
		default:
			met = true
		}
	}
	switch {
	case len(findings) > 0:
		return outcome{status: StatusFinding, findings: findings}
	case len(unknown) > 0:
		return unknownOutcome(strings.Join(unknown, "; "))
	case met:
		return metOutcome()
	}
	return notApplicable(strings.Join(inapplicable, "; "))
}

func appendOnce(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

// evaluator holds what the rules of one Evaluate share.
type evaluator struct {
	in       Input
	paths    map[string]bool
	scoped   []string
	projects []project
}

func (e *evaluator) read(path string) (string, bool) {
	if e.in.Read == nil {
		return "", false
	}
	return e.in.Read(path)
}

func (e *evaluator) finding(practice ID, detail string, subject ...string) Finding {
	p, _ := Lookup(practice)
	return Finding{
		ID: FindingID(practice, subject...), Practice: practice, Subject: strings.Join(subject, ":"),
		Severity: p.Severity, Detail: detail,
	}
}

// Evaluate decides each practice from what the check read. It is pure: it reads no file and runs no command.
func Evaluate(in Input) Result {
	e := &evaluator{in: in, paths: map[string]bool{}}
	for _, path := range in.Paths {
		e.paths[path] = true
		if !IsSkipped(path) {
			e.scoped = append(e.scoped, path)
		}
	}
	e.projects = detect(in.Paths)

	preCommit := e.preCommit()
	outcomes := map[ID]outcome{
		EnvIgnored:        e.envIgnored(),
		Secrets:           e.secrets(),
		CI:                e.ci(preCommit),
		PreCommit:         preCommit,
		Lockfile:          e.lockfile(),
		DependencyUpdates: e.dependencyUpdates(),
		SecurityPolicy:    e.fileIn(SecurityPolicy, "SECURITY.md", "no SECURITY.md in the root, .github/, or docs/"),
		Codeowners:        e.fileIn(Codeowners, "CODEOWNERS", "no CODEOWNERS file in the root, .github/, or docs/"),
		License:           e.license(),
		Tests:             e.tests(),
		BranchProtection:  e.branchProtection(),
		RequiredReview:    e.requiredReview(),
	}

	result := Result{
		Repository: in.Repository, Commit: in.Commit, BaseBranch: in.BaseBranch, Worker: in.Worker,
		CheckedAt: in.CheckedAt, TableVersion: TableVersion, RulesVersion: in.RulesVersion,
		ScannedFrom: in.ScannedFrom, Skipped: in.Skipped,
		Practices: []PracticeStatus{}, Findings: []Finding{},
	}
	for _, p := range table {
		o := outcomes[p.ID]
		status := PracticeStatus{ID: p.ID, Status: o.status}
		if o.status == StatusUnknown || o.status == StatusNotApplicable {
			status.Reason = o.reason
		}
		result.Practices = append(result.Practices, status)
		result.Findings = append(result.Findings, o.findings...)
	}
	sortFindings(result.Findings)
	return result
}

func (e *evaluator) envIgnored() outcome {
	var parts []outcome
	for _, path := range e.in.Paths {
		_, name := splitPath(path)
		if name == ".env" || (strings.HasPrefix(name, ".env.") && name != ".env.example") {
			parts = append(parts, findingOutcome(e.finding(EnvIgnored, fmt.Sprintf("%s is tracked", path), "tracked", path)))
		}
	}
	byFolder := map[string][]string{}
	for _, probe := range Probes(e.in.Paths) {
		folder, _ := splitPath(probe)
		byFolder[folder] = append(byFolder[folder], probe)
	}
	folders := make([]string, 0, len(byFolder))
	for folder := range byFolder {
		folders = append(folders, folder)
	}
	slices.Sort(folders)
	for _, folder := range folders {
		var open, missing []string
		for _, probe := range byFolder[folder] {
			ignored, answered := e.in.Ignored[probe]
			switch {
			case !answered:
				missing = append(missing, probe)
			case !ignored:
				open = append(open, probe)
			}
		}
		switch {
		case len(open) > 0:
			subject := folder
			if subject == "" {
				subject = "."
			}
			parts = append(parts, findingOutcome(e.finding(EnvIgnored,
				"not ignored by the committed .gitignore files: "+strings.Join(open, ", "), "ignore", subject)))
		case len(missing) > 0:
			parts = append(parts, unknownOutcome("no ignore answer for "+strings.Join(missing, ", ")))
		default:
			parts = append(parts, metOutcome())
		}
	}
	return combine(parts, metOutcome())
}

func (e *evaluator) secrets() outcome {
	var findings []Finding
	seen := map[string]bool{}
	for _, m := range e.in.Secrets {
		digest := m.Digest
		if len(digest) > DigestIDLength {
			digest = digest[:DigestIDLength]
		}
		f := e.finding(Secrets, fmt.Sprintf("rule %s matched at %s line %d, commit %s", m.Rule, m.Path, m.Line, shortCommit(m.Commit)), m.Rule, m.Path, digest)
		if seen[f.ID] {
			continue
		}
		seen[f.ID] = true
		f.Rule, f.Path, f.Line, f.Commit, f.Digest = m.Rule, m.Path, m.Line, m.Commit, m.Digest
		findings = append(findings, f)
	}
	switch {
	case len(findings) > 0:
		return findingOutcome(findings...)
	case e.in.SecretsUnknown != "":
		return unknownOutcome(e.in.SecretsUnknown)
	}
	return metOutcome()
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func (e *evaluator) preCommit() outcome {
	if e.paths[".pre-commit-config.yaml"] {
		return metOutcome()
	}
	if e.paths["Makefile"] {
		text, ok := e.read("Makefile")
		if !ok {
			return unknownOutcome("the Makefile cannot be read")
		}
		if makefileHasTarget(text, "hooks") {
			return metOutcome()
		}
	}
	return findingOutcome(e.finding(PreCommit, "no .pre-commit-config.yaml in the root, and no hooks target in the root Makefile"))
}

func (e *evaluator) ci(preCommit outcome) outcome {
	for _, path := range e.in.Paths {
		folder, name := splitPath(path)
		if folder == ".github/workflows" && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) {
			return metOutcome()
		}
	}
	authority := e.in.MergeAuthority
	if authority == "" {
		authority = mergeAuthorityPersonMerges
	}
	recipeAuthority := authority == mergeAuthorityPersonMerges || authority == mergeAuthorityPersonApproves
	if recipeAuthority {
		switch preCommit.status {
		case StatusMet:
			return metOutcome()
		case StatusUnknown:
			return unknownOutcome(preCommit.reason)
		}
	}
	var why []string
	if preCommit.status != StatusMet {
		why = append(why, "the pre-commit practice is not met")
	}
	if !recipeAuthority {
		why = append(why, "merge authority is "+authority+", not person_merges or person_approves")
	}
	return findingOutcome(e.finding(CI, "no GitHub Actions workflow, and the no-CI recipe does not hold: "+strings.Join(why, " and ")))
}

func (e *evaluator) lockfile() outcome {
	parts := make([]outcome, 0, len(e.projects))
	for _, p := range e.projects {
		parts = append(parts, e.lockfileOf(p))
	}
	return combine(parts, notApplicable("no package manager is detected"))
}

func (e *evaluator) lockfileOf(p project) outcome {
	m := p.manager
	if len(m.Lockfiles) == 0 {
		return notApplicable(m.NoLockfile)
	}
	has := func(name string) bool { return slices.Contains(p.manifests, name) }
	manifest := p.manifests[0]
	text, readable := e.read(joinPath(p.folder, manifest))
	switch m.Name {
	case "go":
		if !readable {
			return unknownOutcome("go.mod cannot be read")
		}
		if !goHasRequire(text) {
			return notApplicable(m.NotApplicable)
		}
	case "npm", "composer":
		keys := []string{"dependencies", "devDependencies", "optionalDependencies"}
		if m.Name == "composer" {
			keys = []string{"require", "require-dev"}
		}
		if !readable {
			return unknownOutcome(manifest + " cannot be read")
		}
		declared, err := jsonHasEntries(text, keys...)
		if err != nil {
			return unknownOutcome(manifest + " is not valid JSON")
		}
		if !declared {
			return notApplicable(m.NotApplicable)
		}
	case "python":
		if has("pyproject.toml") && len(p.manifests) == 1 {
			pyproject, ok := e.read(joinPath(p.folder, "pyproject.toml"))
			if !ok {
				return unknownOutcome("pyproject.toml cannot be read")
			}
			if !pyprojectHasDependencies(pyproject) {
				return notApplicable(m.NotApplicable)
			}
		}
		if has("requirements.txt") {
			requirements, ok := e.read(joinPath(p.folder, "requirements.txt"))
			if !ok {
				return unknownOutcome("requirements.txt cannot be read")
			}
			if requirementsPinned(requirements) {
				return metOutcome()
			}
		}
		manifest = pythonLockManifest(p.manifests)
	}
	for folder := p.folder; ; folder, _ = splitPath(folder) {
		for _, lockfile := range m.Lockfiles {
			if e.paths[joinPath(folder, lockfile)] {
				return metOutcome()
			}
		}
		if folder == "" {
			break
		}
	}
	subject := []string{m.Name}
	where := "the root"
	if p.folder != "" {
		subject = append(subject, p.folder)
		where = p.folder
	}
	return findingOutcome(e.finding(Lockfile, fmt.Sprintf("the %s manifest in %s has no lockfile; run `%s`", m.Name, where, m.LockCommandFor(manifest)), subject...))
}

// pythonLockManifest picks the manifest whose lock command a python folder is told to run.
func pythonLockManifest(manifests []string) string {
	for _, name := range []string{"pyproject.toml", "Pipfile", "requirements.txt"} {
		if slices.Contains(manifests, name) {
			return name
		}
	}
	return manifests[0]
}

var renovateConfigs = []string{
	"renovate.json", "renovate.jsonc", "renovate.json5",
	".github/renovate.json", ".github/renovate.jsonc", ".github/renovate.json5",
	".gitlab/renovate.json", ".gitlab/renovate.jsonc", ".gitlab/renovate.json5",
	".renovaterc", ".renovaterc.json", ".renovaterc.jsonc", ".renovaterc.json5",
}

func (e *evaluator) dependencyUpdates() outcome {
	if len(e.projects) == 0 {
		return notApplicable("no package manager is detected")
	}
	for _, path := range append([]string{".github/dependabot.yml", ".github/dependabot.yaml"}, renovateConfigs...) {
		if e.paths[path] {
			return metOutcome()
		}
	}
	return findingOutcome(e.finding(DependencyUpdates, "no .github/dependabot.yml and no Renovate configuration"))
}

// fileIn is met by a file of the name, compared without case, in the root, .github/, or docs/.
func (e *evaluator) fileIn(practice ID, name string, detail string) outcome {
	for _, path := range e.in.Paths {
		folder, base := splitPath(path)
		if (folder == "" || folder == ".github" || folder == "docs") && strings.EqualFold(base, name) {
			return metOutcome()
		}
	}
	return findingOutcome(e.finding(practice, detail))
}

func (e *evaluator) license() outcome {
	f := e.in.Forge
	switch {
	case f == nil:
		return notApplicable("no forge binding")
	case f.VisibilityUnknown != "":
		return unknownOutcome(f.VisibilityUnknown)
	case f.Visibility != VisibilityPublic:
		return notApplicable("the repository is not public")
	}
	for _, path := range e.in.Paths {
		folder, name := splitPath(path)
		if folder != "" {
			continue
		}
		name = strings.ToLower(name)
		for _, base := range []string{"license", "licence", "copying"} {
			if name == base || strings.HasPrefix(name, base+".") {
				return metOutcome()
			}
		}
	}
	return findingOutcome(e.finding(License, "the repository is public and has no LICENSE, LICENCE, or COPYING file in the root"))
}

func (e *evaluator) tests() outcome {
	var parts []outcome
	seen := map[string]bool{}
	for _, p := range e.projects {
		if seen[p.manager.Name] {
			continue
		}
		seen[p.manager.Name] = true
		found := false
		for _, path := range e.scoped {
			if p.manager.isTest(path, e.read) {
				found = true
				break
			}
		}
		if found {
			parts = append(parts, metOutcome())
			continue
		}
		parts = append(parts, findingOutcome(e.finding(Tests, fmt.Sprintf("no tracked %s test file; the convention is %s", p.manager.Name, p.manager.Tests), p.manager.Name)))
	}
	return combine(parts, notApplicable("no package manager is detected"))
}

func (e *evaluator) branchProtection() outcome {
	f := e.in.Forge
	switch {
	case f == nil:
		return notApplicable("no forge binding")
	case f.ProtectedUnknown != "":
		return unknownOutcome(f.ProtectedUnknown)
	case f.Protected:
		return metOutcome()
	}
	return findingOutcome(e.finding(BranchProtection, fmt.Sprintf("the base branch %s has no branch protection and no ruleset rule", e.in.BaseBranch)))
}

func (e *evaluator) requiredReview() outcome {
	f := e.in.Forge
	switch {
	case f == nil:
		return notApplicable("no forge binding")
	case f.ReviewUnknown != "":
		return unknownOutcome(f.ReviewUnknown)
	case f.ReviewCount >= 1:
		return metOutcome()
	}
	return findingOutcome(e.finding(RequiredReview, fmt.Sprintf("the base branch %s does not require an approving review before a merge", e.in.BaseBranch)))
}
