package cli

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/practices"
)

// practicesPoll is how often `practices` reads the requests it made, until each is reported or has expired.
var practicesPoll = time.Second

// practicesCommand asks the workers that keep each repository's checkout to run the practices check, waits for each
// request to be reported or to expire, and prints the latest result of every repository asked of, whether or not this
// run reached it. With --latest it asks for nothing. It exits 0 whatever the check finds: a finding blocks nothing.
func (c *invocation) practicesCommand(args []string) int {
	set := c.flags()
	repository := set.String("repository", "", "check one repository, by name, instead of every active repository")
	latest := set.Bool("latest", false, "print the latest result without asking for a check")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	var (
		reasons map[string]string
		notes   []string
	)
	if !*latest {
		var named []string
		if *repository != "" {
			named = []string{*repository}
		}
		checked, err := client.CheckPractices(c.ctx, named)
		if err != nil {
			return c.failure(err)
		}
		reasons = map[string]string{}
		waiting := map[string]api.PracticesCheckEntry{}
		for _, entry := range checked.Checks {
			if entry.Request == nil {
				reasons[entry.Repository] = entry.Reason
				continue
			}
			waiting[entry.Request.ID] = entry
		}
		if len(waiting) > 0 {
			c.say("Asked %s to run the practices check.\n", api.Count(int64(len(waiting)), "worker", "workers"))
		}
		var code int
		if notes, code = c.awaitPractices(client, waiting); code != ExitOK {
			return code
		}
	}
	list, err := client.Practices(c.ctx, *repository)
	if err != nil {
		return c.failure(err)
	}
	if list.Repositories == nil {
		list.Repositories = []api.PracticesView{}
	}
	return c.finish(nil, list, func() string {
		var out strings.Builder
		for _, note := range notes {
			out.WriteString(note + "\n")
		}
		now := time.Now()
		for _, view := range list.Repositories {
			out.WriteString(practicesText(view, reasons[view.Repository], now))
		}
		return out.String()
	})
}

// awaitPractices reads each request once every practicesPoll until it is reported or has expired, and returns what
// to tell of those that did not produce a result: one that expired, and one whose report left the latest result as it
// was.
func (c *invocation) awaitPractices(client *api.ProjectClient, waiting map[string]api.PracticesCheckEntry) ([]string, int) {
	var notes []string
	for len(waiting) > 0 {
		for _, id := range slices.Sorted(maps.Keys(waiting)) {
			request, err := client.PracticesRequest(c.ctx, id)
			if err != nil {
				return nil, c.failure(err)
			}
			entry := waiting[id]
			switch request.State {
			case ledger.PracticesExpired:
				notes = append(notes, plainText(fmt.Sprintf("the check on %s did not report within 30 minutes; a later report still becomes the latest result", request.Worker)))
			case ledger.PracticesReported:
				view, err := client.Practices(c.ctx, entry.Repository)
				if err != nil {
					return nil, c.failure(err)
				}
				if len(view.Repositories) == 1 && (view.Repositories[0].Result == nil || view.Repositories[0].Result.Request != id) {
					notes = append(notes, plainText(fmt.Sprintf("the check on %s reported no result for %s", request.Worker, cmp.Or(entry.Name, entry.Repository))))
				}
			default:
				continue
			}
			delete(waiting, id)
		}
		if len(waiting) == 0 {
			break
		}
		select {
		case <-c.ctx.Done():
			return nil, c.failure(c.ctx.Err())
		case <-time.After(practicesPoll):
		}
	}
	return notes, ExitOK
}

// practicesText is what `practices` prints for one repository: a header line, the open findings by severity with why
// and fix, the acknowledged findings apart, the practices the check could not settle, and one line naming those met.
// A repository with no result reads as not checked yet, and reason says why no check was requested of it. Every text
// the check read from a repository goes through plainText.
func practicesText(view api.PracticesView, reason string, now time.Time) string {
	name := plainText(cmp.Or(view.Name, view.Repository))
	result := view.Result
	if result == nil {
		why := plainText(reason)
		switch {
		case why != "":
		case view.Request != nil:
			why = "a check was requested on " + plainText(view.Request.Worker) + " and has not reported"
		default:
			why = "no check has reported"
		}
		return fmt.Sprintf("%s not checked yet: %s\n", name, why)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s at %s (base %s), checked %s ago on %s\n", name, plainText(practicesCommit(result.Commit)), plainText(result.BaseBranch),
		api.AgeWords(now.Sub(result.CheckedAt).Milliseconds()), plainText(result.Worker))
	acknowledged := map[string]api.PracticesAcknowledgement{}
	for _, ack := range view.Acknowledgement {
		acknowledged[ack.Finding] = ack
	}
	findings := slices.Clone(result.Findings)
	slices.SortStableFunc(findings, func(a, b api.PracticeFindingView) int { return severityRank(a.Severity) - severityRank(b.Severity) })
	var kept []api.PracticeFindingView
	for _, finding := range findings {
		if _, done := acknowledged[finding.ID]; done {
			kept = append(kept, finding)
			continue
		}
		fmt.Fprintf(&out, "%s  %s\n", finding.Severity, plainText(finding.ID))
		fmt.Fprintf(&out, "  %s\n", plainText(finding.Detail))
		if finding.Path != "" {
			fmt.Fprintf(&out, "  at %s:%d (%s, commit %s)\n", plainText(finding.Path), finding.Line, plainText(finding.Rule), plainText(finding.Commit))
		}
		fmt.Fprintf(&out, "  Why: %s\n  Fix: %s\n", plainText(finding.Why), plainText(finding.Fix))
		if finding.Command != "" {
			fmt.Fprintf(&out, "  Template: %s\n", plainText(finding.Command))
		}
	}
	if len(kept) > 0 {
		out.WriteString("acknowledged:\n")
		for _, finding := range kept {
			ack := acknowledged[finding.ID]
			fmt.Fprintf(&out, "%s  %s: %s (%s, %s)\n", finding.Severity, plainText(finding.ID), plainText(ack.Reason), plainText(ack.Member), ack.AcknowledgedAt.UTC().Format(time.DateOnly))
		}
	}
	var met []string
	for _, status := range result.Practices {
		switch status.Status {
		case practices.StatusUnknown:
			fmt.Fprintf(&out, "unknown: %s: %s\n", status.ID, plainText(status.Reason))
		case practices.StatusNotApplicable:
			fmt.Fprintf(&out, "not applicable: %s: %s\n", status.ID, plainText(status.Reason))
		case practices.StatusMet:
			met = append(met, string(status.ID))
		}
	}
	if len(met) > 0 {
		fmt.Fprintf(&out, "met: %s\n", strings.Join(met, ", "))
	}
	return out.String()
}

func severityRank(severity practices.Severity) int {
	switch severity {
	case practices.High:
		return 0
	case practices.Medium:
		return 1
	}
	return 2
}

// practicesCommit is the seven characters of a commit that git abbreviates it to.
func practicesCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
