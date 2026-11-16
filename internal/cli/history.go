package cli

import "ropeshift.com/ropeshift/internal/api"

// measuresHistory prints the delivery history a planning session sees in its context pack: the project's finished
// work by kind and size, and by kind, size, capability class, and repository (FR-EST-12).
func (c *invocation) measuresHistory(args []string) int {
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
	view, err := client.DeliveryHistory(c.ctx)
	return c.finish(err, view, func() string { return deliveryHistoryText(view) })
}

// deliveryHistoryText prints the history in the words the planning session's pack holds it in, so a person reads what
// the session reads.
func deliveryHistoryText(view api.DeliveryHistory) string {
	return view.Text(view.Repositories)
}
