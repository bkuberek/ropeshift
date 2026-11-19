package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

// maxSkipReason is the longest reason a skip takes, in characters.
const maxSkipReason = 2000

// createFailed is the outcome of a repository creation that stopped at a step that failed.
const createFailed ledger.RepositoryOutcome = "create_failed"

// planRetry runs the creation of one repository a plan version names again, as one entry of a run under the version's
// approval, and prints the entry's new line.
func (c *invocation) planRetry(args []string) int {
	set := c.flags()
	version := set.Int64("version", 0, "the approved plan version that names the repository, the newest that holds it uncreated by default")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 2 {
		return c.usageError("takes one plan ID and one repository name")
	}
	plan, name := positional[0], positional[1]
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	approval, code := c.creationApproval(client, plan, name, *version)
	if approval == "" {
		return code
	}
	decided, err := client.RetryRepository(c.ctx, approval, name)
	return c.finish(err, decided, func() string { return creationText(decided, name) })
}

// planSkip records that a member chose to leave one repository a plan version names uncreated, and prints the entry's
// new line.
func (c *invocation) planSkip(args []string) int {
	set := c.flags()
	version := set.Int64("version", 0, "the approved plan version that names the repository, the newest that holds it uncreated by default")
	var request api.SkipRepository
	set.StringVar(&request.Reason, "reason", "", "why the repository stays uncreated, 1 to 2000 characters")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 2 {
		return c.usageError("takes one plan ID and one repository name")
	}
	if length := utf8.RuneCountInString(request.Reason); length == 0 || length > maxSkipReason {
		return c.usageError("--reason is required and takes 1 to 2000 characters")
	}
	plan, name := positional[0], positional[1]
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	approval, code := c.creationApproval(client, plan, name, *version)
	if approval == "" {
		return code
	}
	decided, err := client.SkipRepository(c.ctx, approval, name, request)
	return c.finish(err, decided, func() string { return creationText(decided, name) })
}

// creationApproval finds the approval a retry or a skip of one repository acts under: the approval of the version a
// member named, which must be approved, or else of the newest approved version whose registration holds the repository
// uncreated. It returns the approval's ID, or the exit code of the refusal it printed.
func (c *invocation) creationApproval(client *api.ProjectClient, plan, name string, version int64) (string, int) {
	if version != 0 {
		view, err := client.PlanVersion(c.ctx, plan, version)
		if err != nil {
			return "", c.failure(err)
		}
		if view.Approval == nil || view.Approval.Approval.State != authority.ApprovalApproved {
			return "", c.refusal(api.CodeIllegalMove, "version %d of plan %s is not an approved version, and only an approved version creates repositories", version, plan)
		}
		return view.Approval.Approval.ID, ExitOK
	}
	view, err := client.Plan(c.ctx, plan)
	if err != nil {
		return "", c.failure(err)
	}
	for _, line := range view.Versions {
		if line.State != authority.ApprovalApproved {
			continue
		}
		held, err := client.PlanVersion(c.ctx, plan, line.Version)
		if err != nil {
			return "", c.failure(err)
		}
		if held.Approval != nil && holdsUncreated(held.Approval.Registration, name) {
			return held.Approval.Approval.ID, ExitOK
		}
	}
	return "", c.refusal(api.CodeIllegalMove, "no approved version of plan %s holds repository %s uncreated; name a version with --version", plan, name)
}

// holdsUncreated says a registration records the repository as refused, not attempted, or failed to create.
func holdsUncreated(registration *api.ManifestRegistration, name string) bool {
	if registration == nil {
		return false
	}
	for _, one := range registration.Repositories {
		if one.Name != name {
			continue
		}
		switch one.Outcome {
		case ledger.RegistrationRefused, ledger.NotAttempted, createFailed:
			return true
		}
	}
	return false
}

// creationText prints the line of the entry a retry or a skip acted on, as the approval prints it, and the problem the
// run could not get past, when it had one.
func creationText(decided api.ApprovalDecided, name string) string {
	var out strings.Builder
	for _, one := range decided.Repositories {
		if one.Name == name {
			out.WriteString(manifestText([]api.RepositoryAdded{one}, false, ""))
		}
	}
	if decided.RepositoriesProblem != "" {
		fmt.Fprintf(&out, "%s\n", decided.RepositoriesProblem)
	}
	return out.String()
}
