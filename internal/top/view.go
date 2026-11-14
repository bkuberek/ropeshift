package top

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/workorders"
)

// plainWidth is the width of a frame with no terminal to measure.
const plainWidth = 120

const help = "j/k move   r read now   q quit"

// clean makes a value from the ledger safe to print. Titles, reasons, and IDs can come from agents and
// repositories, and a terminal obeys the control sequences it is sent, so one could rewrite the screen
// or the clipboard. Format characters go too, because one can turn a line around.
func clean(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r == utf8.RuneError, unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r):
			return '?'
		}
		return r
	}, text)
}

// age states how long ago a moment was, in the largest unit that says something.
func age(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return strconv.Itoa(int(d/time.Second)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

func clip(line string, width int) string {
	if width <= 0 || utf8.RuneCountInString(line) <= width {
		return line
	}
	return string([]rune(line)[:width-1]) + "~"
}

// actionCommands names what each action of a work item runs. The TUI only reads, so this is how an
// operator gets from a line to the act.
var actionCommands = map[string]string{
	api.ActReassign: "work reassign %s --reason R",
	api.ActResolve:  "work resolve %s --reason R",
	api.ActReplan:   "work replan %s --reason R",
	api.ActReset:    "lane reset %s",
	api.ActRespawn:  "lane respawn %s",
	api.ActResume:   "lane resume %s",
	api.ActAccept:   "work accept %s",
	api.ActRework:   "work rework %s --reason R",
	api.ActCancel:   "work cancel %s --reason R",
}

// Actions lists what this view can name a command for.
func Actions() []string {
	actions := []string{api.ActStartOrchestrator}
	for action := range actionCommands {
		actions = append(actions, action)
	}
	slices.Sort(actions)
	return actions
}

// CommandFor is the command an action names, for one item.
func CommandFor(action string, item api.NeedItem) (string, bool) {
	if action == api.ActStartOrchestrator && item.Work != nil && item.Work.Orchestrator != nil {
		return "ropeshift orchestrator start " + clean(item.Work.Orchestrator.WorkerID), true
	}
	format, ok := actionCommands[action]
	if !ok {
		return "", false
	}
	return "ropeshift " + fmt.Sprintf(format, clean(item.ID)), true
}

// itemCommands is what a person can do about one item, in the project it waits in.
func itemCommands(item api.NeedItem) []string {
	commands := commandsFor(item)
	if item.Project == nil {
		return commands
	}
	for i, command := range commands {
		if !strings.HasPrefix(command, "ropeshift orchestrator ") {
			commands[i] = command + " --project " + projectOf(item)
		}
	}
	return commands
}

func commandsFor(item api.NeedItem) []string {
	switch item.Kind {
	case api.NeedQuestions:
		return []string{"ropeshift question batches"}
	case api.NeedApproval:
		return []string{"ropeshift approval show " + clean(item.ID)}
	case api.NeedEffect:
		return []string{"ropeshift effect show " + clean(item.ID)}
	case api.NeedPlanApproval:
		if item.PlanApproval == nil {
			return nil
		}
		plan := clean(item.PlanApproval.Approval.Plan)
		return []string{"ropeshift plan show " + plan, fmt.Sprintf("ropeshift plan approve %s --version %d", plan, item.PlanApproval.Version)}
	case api.NeedContractReview:
		return []string{"ropeshift work review " + clean(item.ID)}
	case api.NeedConfirm:
		return []string{"ropeshift confirm " + clean(item.ID), "ropeshift decline " + clean(item.ID)}
	}
	if item.Work == nil {
		return nil
	}
	var commands []string
	for _, action := range item.Work.Actions {
		if command, ok := CommandFor(action, item); ok {
			commands = append(commands, command)
		}
	}
	return commands
}

// reasonWords says the reasons whose names are codes, and the rest read as they are.
var reasonWords = map[string]string{
	api.ReasonResume:     "parked",
	api.ReasonLongParked: "long parked",
}

// waitsFor says what a parked lane's wakes wait for, in the short words of the web UI's.
func waitsFor(wakes []api.WakeView) string {
	said := make([]string, 0, len(wakes))
	for _, wake := range wakes {
		expr, err := parking.Parse(wake.Expression, "")
		if err != nil {
			said = append(said, "something this build cannot read")
			continue
		}
		said = append(said, expr.ShortWords())
	}
	return strings.Join(said, "; ")
}

func what(item api.NeedItem) string {
	switch {
	case item.Batch != nil:
		return strconv.Itoa(len(item.Batch.Items)) + " to answer"
	case item.Approval != nil && item.Approval.Kind == authority.KindMerge:
		detail := authority.MergeDetailOf(*item.Approval)
		if item.Approval.Subject == authority.MergeHeld {
			return fmt.Sprintf("the merge of %s is held: %s", clean(item.Approval.WorkOrder), clean(detail.Reason))
		}
		return fmt.Sprintf("approve the merge of %s at %s", clean(item.Approval.WorkOrder), clean(item.Approval.SubjectDigest))
	case item.Approval != nil && item.Approval.Kind == authority.KindRecovery:
		detail := authority.RecoveryDetailOf(*item.Approval)
		return fmt.Sprintf("the orchestrator asks to %s %s: %s; outside the policy: %s",
			clean(item.Approval.Subject), clean(item.Approval.WorkOrder), clean(detail.Reason), clean(detail.Limit))
	case item.Approval != nil:
		return "lane " + clean(item.Approval.Lane) + " asks about " + clean(item.Approval.Subject)
	case item.Effect != nil:
		return "a crash left " + clean(item.Effect.Kind) + " unknown"
	case item.PlanApproval != nil:
		need := item.PlanApproval
		return fmt.Sprintf("plan %s v%d waits: %s", clean(need.Title), need.Version, clean(need.Summary))
	case item.ContractReview != nil:
		need := item.ContractReview
		return fmt.Sprintf("contract review: %s version %d, work %s", clean(need.Contract), need.Version, clean(need.WorkOrder.Title))
	case item.Card != nil:
		return clean(item.Card.Approval.Summary) + "  prepared by " + preparer(*item.Card)
	case item.Work == nil:
		return ""
	}
	line := clean(item.Work.Reason)
	if said, ok := reasonWords[item.Work.Reason]; ok {
		line = said
	}
	if len(item.Work.Wakes) != 0 {
		line += ": waits for " + clean(waitsFor(item.Work.Wakes))
	}
	if item.Work.WorkOrder != nil && item.Work.WorkOrder.Title != "" {
		line += "  " + clean(item.Work.WorkOrder.Title)
	}
	if item.Work.Orchestrator != nil {
		line += "  orchestrator of " + clean(item.Work.Orchestrator.WorkerID)
	}
	if found := reviewFound(item.Work.Standing); found != "" {
		line += "  " + found
	}
	if item.Work.Detail != "" {
		line += "  " + clean(item.Work.Detail)
	}
	return line
}

// preparer names the delegate that prepared a card, by its credential's label and the member it acts for, as
// Needs you does.
func preparer(card api.CardNeed) string {
	if card.Member == "" {
		return clean(card.Label)
	}
	return clean(card.Label) + " for " + clean(card.Member)
}

// reviewFound counts the verdicts a review gave, in the words a person reads in the web UI. Work with none says nothing.
func reviewFound(standing *workorders.AcceptanceView) string {
	if standing == nil || len(standing.Verdicts) == 0 {
		return ""
	}
	var counts [3]int
	for _, v := range standing.Verdicts {
		switch v.Verdict {
		case workorders.VerdictPass:
			counts[0]++
		case workorders.VerdictFail:
			counts[1]++
		case workorders.VerdictCannotJudge:
			counts[2]++
		}
	}
	var parts []string
	for i, word := range []string{"pass", "fail", "cannot judge"} {
		if counts[i] != 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[i], word))
		}
	}
	return "review: " + strings.Join(parts, ", ")
}

func (m Model) ago(ms int64) time.Duration { return m.config.Now().Sub(time.UnixMilli(ms)) }

func (m Model) freshness(value string, at int64) string {
	if value == ledger.FreshnessObserved {
		return "observed"
	}
	if at == 0 {
		return "last reported"
	}
	return "last reported " + age(m.ago(at)) + " ago"
}

func (m Model) header() string {
	line := fmt.Sprintf("ropeshift top   %d need you   %s   %s", len(m.snapshot.Needs.Items),
		api.Count(int64(len(m.snapshot.Floor.Lanes)), "lane", "lanes"), api.Count(int64(len(m.snapshot.Floor.Workers)), "worker", "workers"))
	if !m.readAt.IsZero() {
		line += "   read " + age(m.config.Now().Sub(m.readAt)) + " ago"
	}
	if m.failure != nil {
		line += "   " + clean(m.failure.Error())
	}
	return line
}

// needLines gives each need a line, with the project it waits in, and the index of the line the cursor stands
// on. The project and kind columns are as wide as the widest listed, so every ID starts in one column. What a
// need says can be longer than the room left on its line, and it wraps onto lines under the need, in the
// column it started in, so a frame as wide as `width` clips none of it.
func (m Model) needLines(width int) (lines []string, cursorLine int) {
	items := m.snapshot.Needs.Items
	kinds, projects := 0, 0
	for _, item := range items {
		kinds = max(kinds, utf8.RuneCountInString(clean(item.Kind)))
		projects = max(projects, utf8.RuneCountInString(projectOf(item)))
	}
	lines = make([]string, 0, len(items))
	for i, item := range items {
		mark := " "
		if i == m.cursor {
			mark = ">"
			cursorLine = len(lines)
		}
		project := ""
		if projects > 0 {
			project = fmt.Sprintf("%-*s ", projects, projectOf(item))
		}
		head := fmt.Sprintf("%s %5s  %s%-*s %s  ", mark, age(m.ago(item.Since)), project, kinds, clean(item.Kind), clean(item.ID))
		lines = append(lines, wrapAfter(head, what(item), width)...)
	}
	return lines, cursorLine
}

// minRoom is the least room beside a need's head that its words start in. A head that leaves less gives
// all of them the lines under it.
const minRoom = 20

// wrapAfter sets text after a head, and wraps what does not fit within width columns onto lines indented to
// where the text began, or by four columns when the head leaves too little room to begin beside it. A word
// longer than a line breaks where the line does. A width of zero or less wraps nothing.
func wrapAfter(head, text string, width int) []string {
	runes := utf8.RuneCountInString
	if width <= 0 || runes(head)+runes(text) <= width {
		return []string{strings.TrimRight(head+text, " ")}
	}
	var lines []string
	prefix, indent := head, runes(head)
	if width-indent < minRoom {
		lines = append(lines, strings.TrimRight(head, " "))
		indent = min(4, width/2)
		prefix = strings.Repeat(" ", indent)
	}
	pad, room := strings.Repeat(" ", indent), max(1, width-indent)
	var body string
	flush := func() {
		lines = append(lines, strings.TrimRight(prefix+body, " "))
		prefix, body = pad, ""
	}
	for _, word := range strings.Split(text, " ") {
		if word == "" && body == "" {
			continue
		}
		for runes(word) > room {
			if body != "" {
				flush()
			}
			cut := []rune(word)
			body, word = string(cut[:room]), string(cut[room:])
			flush()
		}
		if body != "" && runes(body)+1+runes(word) > room {
			flush()
		}
		if body == "" {
			body = word
		} else {
			body += " " + word
		}
	}
	if body != "" {
		flush()
	}
	return lines
}

// projectOf names the project an item of the combined Needs you waits in, or nothing for an item read
// from one project.
func projectOf(item api.NeedItem) string {
	if item.Project == nil {
		return ""
	}
	return clean(item.Project.Name)
}

// laneLines gives each lane a line that says what it is and how it stands, and under it, when there are any,
// its figures: its context, tokens, and cost, and its process tree. Apart, each fits a printed frame.
func (m Model) laneLines() []string {
	lanes := LaneOrder(m.snapshot)
	figures := map[string]api.LaneTelemetry{}
	for _, lane := range m.snapshot.Telemetry.Lanes {
		figures[lane.Lane] = lane
	}
	sessions := m.sessions()
	lines := make([]string, 0, 2*len(lanes))
	for _, lane := range lanes {
		conditions := strings.Join(lane.Conditions, ",")
		if conditions == "" {
			conditions = "-"
		}
		lines = append(lines, fmt.Sprintf("  %s  %s  %s  liveness %s  model %s  ledger %s  %s  %s", clean(lane.ID), clean(lane.WorkOrder), clean(lane.ActualState),
			clean(lane.Liveness), clean(lane.ModelReach), clean(lane.LedgerReach), clean(conditions), m.freshness(lane.Freshness, lane.AsOf)))
		var measured []string
		if figure, ok := figures[lane.ID]; ok {
			measured = append(measured, fmt.Sprintf("context %d  tokens %d  cost %s", figure.ContextTokens, laneTokens(figure), api.Dollars(figure.CostUSD)))
		}
		if session, ok := sessions[lane.ID]; ok {
			measured = append(measured, api.SessionWords(session))
		}
		if len(measured) != 0 {
			lines = append(lines, "    "+strings.Join(measured, "  "))
		}
	}
	return lines
}

// laneTokens sums every type, the way a person reads a lane's total.
func laneTokens(figure api.LaneTelemetry) int64 {
	var total int64
	for _, tokens := range figure.Tokens {
		total += tokens
	}
	return total
}

// workerLines gives each worker a line, with its orchestrator and that orchestrator's process tree when it
// reported one, under it what it last measured of its machine, and under that when, with any warning
// (FR-UI-10).
func (m Model) workerLines() []string {
	lines := make([]string, 0, 3*len(m.snapshot.Floor.Workers))
	for _, line := range m.snapshot.Floor.Workers {
		report := line.Worker.Report
		text := fmt.Sprintf("  %s  %s  epoch %d  %s", clean(line.Worker.ID), clean(line.Worker.Name), line.Worker.Epoch, m.freshness(line.Freshness, 0))
		if line.Orchestrator != nil {
			actual := clean(line.Orchestrator.Actual)
			if actual == "" {
				actual = "not reported"
			}
			text += fmt.Sprintf("  orchestrator %s, wanted %s", actual, clean(line.Orchestrator.Desired))
		}
		if report != nil {
			for _, session := range report.Sessions {
				switch {
				case session.Kind != ledger.SessionOrchestrator:
				case line.Orchestrator != nil:
					text += ", " + api.SessionWords(session)
				default:
					text += "  orchestrator " + api.SessionWords(session)
				}
			}
		}
		lines = append(lines, text, "    "+api.MachineWords(report))
		if report != nil {
			measured := "    measured " + age(m.ago(line.Worker.ReportedAt)) + " ago"
			if line.ReportFreshness != ledger.FreshnessObserved {
				measured += ", last reported"
			}
			if warnings := api.WarningWords(report); warnings != "" {
				measured += "  " + warnings
			}
			lines = append(lines, measured)
		}
	}
	return lines
}

// sessions is each lane's process tree as its worker last reported it, by lane.
func (m Model) sessions() map[string]ledger.SessionResources {
	out := map[string]ledger.SessionResources{}
	for _, line := range m.snapshot.Floor.Workers {
		if line.Worker.Report == nil {
			continue
		}
		for _, session := range line.Worker.Report.Sessions {
			if session.Kind == ledger.SessionLane {
				out[session.Lane] = session
			}
		}
	}
	return out
}

// window keeps the cursor in view when a list has less room than it has lines.
func window(lines []string, cursor, room int) []string {
	if room >= len(lines) {
		return lines
	}
	start := max(0, min(cursor-room/2, len(lines)-room))
	return lines[start : start+room]
}

// View is the frame: what needs a person first, then the lanes in the same order, then the workers, and the
// keys at its foot. A terminal too short for all of it loses the bottom first, and never the cursor.
func (m Model) View() string {
	return m.frame(true)
}

// frame is the view, with the keys at its foot when it takes them. A printed frame takes none.
func (m Model) frame(keys bool) string {
	width := m.width
	if width <= 0 {
		width = plainWidth
	}
	needs, cursorLine := m.needLines(width)
	if len(needs) == 0 {
		needs = []string{"  Nothing needs you."}
	}
	var commands []string
	if m.cursor < len(m.snapshot.Needs.Items) {
		for _, command := range itemCommands(m.snapshot.Needs.Items[m.cursor]) {
			commands = append(commands, "    "+command)
		}
	}
	lanes := m.laneLines()
	if len(lanes) == 0 {
		lanes = []string{"  No lanes."}
	}
	rest := append(append(append([]string{"LANES"}, lanes...), "WORKERS"), m.workerLines()...)
	if m.height > 0 {
		// A header, a title, the commands, and the help have to fit, so the list gets what is left.
		room := max(1, m.height-3-len(commands))
		needs = window(needs, cursorLine, room)
		rest = rest[:max(0, min(len(rest), m.height-3-len(commands)-len(needs)))]
	}
	frame := append([]string{m.header(), "NEEDS YOU"}, needs...)
	frame = append(append(frame, commands...), rest...)
	if keys {
		frame = append(frame, help)
	}
	for i, line := range frame {
		frame[i] = clip(line, width)
	}
	return strings.Join(frame, "\n")
}
