package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

func dollars(amount float64) string { return fmt.Sprintf("$%.2f", amount) }

// limitText prints a limit, and says so when there is none.
func limitText(amount float64) string {
	if amount <= 0 {
		return "none"
	}
	return dollars(amount)
}

func spendKeyLines(out *strings.Builder, keys []api.SpendKey) {
	for _, key := range keys {
		out.WriteString(key.Key + "\t" + key.Value + "\n")
	}
}

func spendPolicyHeader(title string, view api.SpendPolicyView) string {
	return fmt.Sprintf("%s\tversion %d\t%s\n", title, view.Version, view.AuthoredBy)
}

func spendShowText(view api.SpendView) string {
	var out strings.Builder
	out.WriteString(spendPolicyHeader("spend policy", view.Project))
	spendKeyLines(&out, view.Project.Keys)
	out.WriteString(spendPolicyHeader("installation spend policy", view.Installation))
	spendKeyLines(&out, view.Installation.Keys)
	out.WriteString(projectWindowText(view.ProjectSpend.Day))
	out.WriteString(projectWindowText(view.ProjectSpend.Week))
	out.WriteString("open work orders\tcharged\tsoft limit\thard limit\tconditions\n")
	for _, order := range view.WorkOrders {
		fmt.Fprintf(&out, "%s\t%s\t%s\t%s\t%s\t%s\n", order.WorkOrder, order.Kind, dollars(order.Charged.ChargedUSD), limitText(order.SoftLimit),
			limitText(order.HardLimit), strings.Join(order.Conditions, ","))
	}
	return out.String()
}

// projectWindowText says where a project stands in a day or a week: what its lanes were charged, and the limits it runs
// under, which a member raised when they say so (FR-SPEND-6).
func projectWindowText(w ledger.ProjectWindowSpend) string {
	line := fmt.Sprintf("project %s\tspent %s\tsoft limit %s\thard limit %s", w.Window, dollars(w.SpentUSD), limitText(w.SoftLimit), limitText(w.HardLimit))
	if w.RaisedTo > 0 {
		line += "\traised by a member"
	}
	if w.Past() {
		line += "\tpast its limit until " + time.UnixMilli(w.EndsAt).UTC().Format(time.RFC3339)
	}
	return line + "\n"
}

func workOrderSpendText(spend ledger.WorkOrderSpend) string {
	var out strings.Builder
	fmt.Fprintf(&out, "work order %s (%s)\n", spend.WorkOrder, spend.Kind)
	fmt.Fprintf(&out, "charged\t%s (%s)\n", dollars(spend.Charged.ChargedUSD), spend.Label)
	if spend.Charged.Unpriced > 0 {
		fmt.Fprintf(&out, "unpriced\t%d turns of a lane with no known turn, charged nothing\n", spend.Charged.Unpriced)
	}
	fmt.Fprintf(&out, "soft limit\t%s\nhard limit\t%s\n", limitText(spend.SoftLimit), limitText(spend.HardLimit))
	if spend.HardLimit > 0 {
		fmt.Fprintf(&out, "remaining\t%s\n", dollars(spend.Remaining))
	}
	if spend.Basis != "" {
		fmt.Fprintf(&out, "basis\t%s\n", spend.Basis)
	}
	fmt.Fprintf(&out, "policy\tversion %d\n", spend.PolicyVersion)
	if len(spend.Conditions) > 0 {
		fmt.Fprintf(&out, "conditions\t%s\n", strings.Join(spend.Conditions, ","))
	}
	return out.String()
}

// spend shows where spend stands, or one work order's charged spend with --work-order.
func (c *invocation) spend(args []string) int {
	set := c.flags()
	workOrder := set.String("work-order", "", "show this work order's charged spend against its limits")
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
	if *workOrder != "" {
		spend, err := client.WorkOrderSpend(c.ctx, *workOrder)
		return c.finish(err, spend, func() string { return workOrderSpendText(spend) })
	}
	view, err := client.Spend(c.ctx)
	return c.finish(err, view, func() string { return spendShowText(view) })
}

func (c *invocation) spendShow(args []string) int {
	set := c.flags()
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
	view, err := client.Spend(c.ctx)
	return c.finish(err, view, func() string { return spendShowText(view) })
}

// spendEdit makes the body of the version after the one in force from the one in force, and says whether it differs.
// An error is a refusal the caller's words earned.
type spendEdit func(current authority.PolicyVersion) (body string, changed bool, err error)

// publishSpend publishes the version an edit makes, through the same route and rules as `policy publish`, so a
// delegate's call is a card and every limit on a policy holds. An edit that changes nothing records nothing.
func (c *invocation) publishSpend(kind authority.PolicyKind, edit spendEdit) int {
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	versions, err := client.PoliciesOf(c.ctx, kind)
	if err != nil {
		return c.failure(err)
	}
	if len(versions) == 0 {
		return c.usageError("no version of the %s policy is in force", kind)
	}
	body, changed, err := edit(versions[0])
	if err != nil {
		return c.usageError("%v", err)
	}
	if !changed {
		return c.finish(nil, versions[0], func() string { return fmt.Sprintf("unchanged: version %d is in force\n", versions[0].Version) })
	}
	row, err := client.PublishPolicy(c.ctx, body)
	return c.finish(err, row, func() string { return policyLine(row.PolicyVersion) })
}

// setsOf reads `key=value` arguments.
func setsOf(args []string) ([][2]string, error) {
	out := make([][2]string, 0, len(args))
	for _, arg := range args {
		key, value, found := strings.Cut(arg, "=")
		if !found || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%q is no key=value", arg)
		}
		out = append(out, [2]string{strings.TrimSpace(key), value})
	}
	return out, nil
}

// spendSet publishes a new version from the one in force with only the keys named changed.
func (c *invocation) spendSet(args []string) int {
	set := c.flags()
	installation := set.Bool("installation", false, "change the installation's spend policy, which the orchestrator serving every project reads")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) == 0 {
		return c.usageError("name at least one key=value")
	}
	changes, err := setsOf(positional)
	if err != nil {
		return c.usageError("%v", err)
	}
	if *installation {
		return c.publishSpend(authority.PolicyInstallationSpend, func(current authority.PolicyVersion) (string, bool, error) {
			policy, err := authority.ReadInstallationSpendPolicy(current)
			if err != nil {
				return "", false, err
			}
			next := policy
			for _, change := range changes {
				if next, err = next.Set(change[0], change[1]); err != nil {
					return "", false, err
				}
			}
			if next == policy {
				return "", false, nil
			}
			next.Version = current.Version + 1
			return next.Body(), true, nil
		})
	}
	return c.publishSpend(authority.PolicySpend, func(current authority.PolicyVersion) (string, bool, error) {
		policy, err := authority.ReadSpendPolicy(current)
		if err != nil {
			return "", false, err
		}
		next := policy
		for _, change := range changes {
			if next, err = next.Set(change[0], change[1]); err != nil {
				return "", false, err
			}
		}
		same := true
		for _, key := range authority.SpendPolicyKeys {
			same = same && next.Text(key) == policy.Text(key)
		}
		if same {
			return "", false, nil
		}
		next.Version = current.Version + 1
		return next.Body(), true, nil
	})
}

// spendRevert records an earlier version of a spend policy as the newest, so its values are in force again and the
// history keeps every version.
func (c *invocation) spendRevert(args []string) int {
	set := c.flags()
	installation := set.Bool("installation", false, "revert the installation's spend policy")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one version")
	}
	version, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil || version < 1 {
		return c.usageError("version %q is not a version, which is a number from 1", positional[0])
	}
	kind := authority.PolicySpend
	if *installation {
		kind = authority.PolicyInstallationSpend
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	earlier, err := client.PolicyOf(c.ctx, kind, version)
	client.Close()
	if err != nil {
		return c.failure(err)
	}
	return c.publishSpend(kind, func(current authority.PolicyVersion) (string, bool, error) {
		if current.Version == earlier.Version {
			return "", false, fmt.Errorf("version %d is the version in force", earlier.Version)
		}
		if *installation {
			policy, err := authority.ReadInstallationSpendPolicy(earlier)
			if err != nil {
				return "", false, err
			}
			policy.Version = current.Version + 1
			return policy.Body(), true, nil
		}
		policy, err := authority.ReadSpendPolicy(earlier)
		if err != nil {
			return "", false, err
		}
		policy.Version = current.Version + 1
		return policy.Body(), true, nil
	})
}
