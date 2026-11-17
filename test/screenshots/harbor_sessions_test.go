//go:build screenshots

package screenshots

import (
	"path/filepath"
	"strings"
)

// The planning sessions of guides 3 and 4. A person starts each from the web UI, so no lane script can name
// it, and both play the fixture's own turns: guide 3's planning session first, then guide 4's adopt session.
// A turn that both reach plays one part on the session that reaches it first and the other on the one after,
// by a mark the fake CLI leaves beside itself the first time.
//
// Guide 3's session asks a product question at its start, and a second one when it takes the line the person
// types into it, so each reaches Needs you as an item of its own whatever the batch tick does. It writes the
// whole plan with the second answer, and submits it in the same turn, after a pause that leaves the plan on
// the Planning page for the guide to show. A planning lane whose turn ends with no outcome and no blocking
// question open waits for its person, so the session submits in the turn that finishes the plan and never
// leaves the lane waiting. Guide 4's session asks which branches in flight to keep, and writes and submits
// the adoption plan once it has the answer.

// The in-flight branches of the project guide 4 adopts.
const (
	branchCancel  = "feature/cancel-booking"
	branchMapView = "spike/map-view"
)

// The questions the sessions ask, and the options they give. Guide 3 answers with the first option of the
// first question and the second of the second.
const (
	questionClosedBerths = "Should the berths page list berths that are closed for maintenance?"
	questionSailingRange = "How far ahead should a berth's sailings look?"
	questionKeepBranches = "Which branches in flight should the plan keep?"
)

// sessions lays out what guides 3 and 4 need beyond the fresh installation: the folder of an existing
// project's clones, which guide 4 imports, and the turns of both planning sessions, in place of the
// assistant's turns harbor starts with. It answers the variables it adds to the world's.
func (h *harbor) sessions() map[string]string {
	in := h.in
	in.t.Helper()
	folder := filepath.Join(guidesFolder, "moorings")
	api := in.newCloneAt(filepath.Join(folder, "moorings-api"), harborRemoteBase, mooringsAPIFiles())
	inFlight(api, branchCancel, "Start cancelling a booking", map[string]string{
		"internal/bookings/cancel.go": lines("package bookings", "", "// Cancel frees a booked mooring.", "func Cancel() {}"),
	})
	web := in.newCloneAt(filepath.Join(folder, "moorings-web"), harborRemoteBase, mooringsWebFiles())
	inFlight(web, branchMapView, "Try a map of the moorings", map[string]string{
		"src/map.ts": lines("// showMap draws the moorings on a map of the harbor.", "export function showMap(): void {}"),
	})
	in.newCloneAt(filepath.Join(folder, "moorings-docs"), harborRemoteBase, mooringsDocsFiles())
	in.writeFixture(planningSessionTurns())
	return map[string]string{"moorings": folder}
}

// inFlight commits files on a new branch of a clone and pushes it, and leaves the clone on its default
// branch, as a branch a person has not merged yet.
func inFlight(c *clone, branch, message string, files map[string]string) {
	c.T.Helper()
	c.Git("checkout", "--quiet", "-b", branch)
	c.write(files)
	c.Git("add", "-A")
	c.Git("commit", "--quiet", "-m", message)
	c.Git("push", "--quiet", "origin", branch)
	c.Git("checkout", "--quiet", "main")
}

// mooringsAPIFiles is the API of Moorings, which books the harbor's moorings for visiting boats.
func mooringsAPIFiles() map[string]string {
	return map[string]string{
		"README.md": lines("# moorings-api", "", "Bookings of the harbor's moorings, and the API the booking pages call."),
		"go.mod":    "module example.com/moorings-api\n\ngo 1.24\n",
		"internal/bookings/bookings.go": lines("package bookings", "",
			"// Book holds a mooring for a boat for a span of nights.", "func Book() {}"),
		"internal/bookings/bookings_test.go": lines("package bookings", "", "import \"testing\"", "",
			"func TestBookHoldsTheMooring(t *testing.T) { Book() }"),
	}
}

// mooringsWebFiles is the booking pages of Moorings.
func mooringsWebFiles() map[string]string {
	return map[string]string{
		"README.md":    lines("# moorings-web", "", "The pages a skipper books a mooring on."),
		"package.json": lines("{", "  \"name\": \"moorings-web\",", "  \"private\": true", "}"),
		"src/book.ts":  lines("// book sends a booking to the API.", "export async function book(): Promise<void> {}"),
	}
}

// mooringsDocsFiles is the documents of Moorings, written before ropeshift, in no format of its own.
func mooringsDocsFiles() map[string]string {
	return map[string]string{
		"README.md": lines("# moorings-docs", "", "The product documents of Moorings."),
		"AGENTS.md": lines("# Moorings", "",
			"Moorings books the harbor's moorings for visiting boats. Its code is in moorings-api and moorings-web,",
			"and its documents are here. When two documents disagree, docs/requirements.md wins. Plan files go in plan/."),
		"docs/requirements.md": mooringsRequirementsProse,
	}
}

const mooringsRequirementsProse = `# Requirements

- MOR-1: A skipper books a mooring for a span of nights.
- MOR-2: A skipper cancels a booking up to a day before arrival.
- MOR-3: A skipper gets a reminder the day before arrival.
`

// planningSessionTurns are the turns of every session no lane script names, in the order a session reaches
// them. The first two are shared by guide 3's session and guide 4's, which reaches them second.
func planningSessionTurns() [][]step {
	return [][]step{
		shared("planning-session-0", planOpening(), adoptOpening()),
		shared("planning-session-1", planNoted(), adoptWritten()),
		planTold(),
		planWritten(),
	}
}

// planShown is how long guide 3's session pauses before it submits, so the plan it wrote stays on the
// Planning page while the walk reads it. A real session also takes a while to read back what it wrote.
const planShown = 15_000

// shared plays first on the session that reaches this turn first, and second on any session after it. The
// later part comes first in the turn, so the first session does not play it once it has left the mark.
func shared(mark string, first, second []step) []step {
	return []step{
		{"after_first": mark, "then": second},
		{"first_time": mark, "then": first},
	}
}

// ask asks the person one blocking product question, with the options to choose from.
func ask(text string, options ...string) step {
	return step{"mcp": "ask", "arguments": map[string]any{
		"text": text, "categories": []string{"product"}, "blocking": true, "options": options,
	}}
}

// writeLines writes one file in the lane's worktree, line by line. No line holds a single quote.
func writeLines(file string, body ...string) step {
	if strings.Contains(strings.Join(body, ""), "'") {
		panic("a scripted file holds a single quote: " + file)
	}
	return bash("mkdir -p " + filepath.Dir(file) + " && printf '%s\\n' '" + strings.Join(body, "' '") + "' > " + file)
}

// commitAll commits everything the turn wrote on the lane's branch.
func commitAll(message string) step {
	return bash("git add -A && git commit -q -m '" + message + "'")
}

// submitted reports the planning lane's work done, which leaves its branch for a plan approval.
func submitted() step {
	return report("completed", map[string]any{"artifacts": []string{"branch"}})
}

// planOpening is guide 3's first turn: it reads the planning repository, commits the goal, and asks a
// question the plan cannot go on without, then ends its turn.
func planOpening() []step {
	return []step{
		bash("git log --oneline -3"),
		writeLines("plan/goal.md",
			"+++", "kind = \"goal\"", "id = \"berths-on-the-web\"", "title = \"Every berth and its sailings on the public pages\"",
			"success_measures = [\"A visitor finds the next sailing from a berth on one page\"]", "+++", "",
			"Visitors ask the harbor office which berth a ferry leaves from. The public pages should answer that."),
		commitAll("Write the goal"),
		ask(questionClosedBerths, "Show them, marked closed", "Leave them out"),
	}
}

// sailingsDecision is the draft decision the line the person types becomes, in the plan's file.
var sailingsDecision = []string{
	"[[decision]]", "id = \"sailings-under-berths\"", "question = \"Do the sailings get a page of their own?\"",
	"answer = \"No. Each berth shows its sailings under it.\"", "rationale = \"The person said so in the session.\"",
}

// planNoted is the turn of the line the person types: the session starts the plan's file with what they said
// as a draft decision, commits it, asks what that leaves open, and ends its turn.
func planNoted() []step {
	plan := append([]string{"+++", "kind = \"plan\"", "id = \"harbor-v1\"", "title = \"Berths and sailings on the public pages\"",
		"goal = \"berths-on-the-web\"", "documents = []", ""}, sailingsDecision...)
	return []step{
		writeLines("plan/plan.md", append(plan, "+++", "", "The plan of the berths and their sailings.")...),
		commitAll("Draft where the sailings show"),
		ask(questionSailingRange, "The next 24 hours", "The next 7 days"),
	}
}

// planTold is the turn of the first answer. The session notes it and ends its turn, because the second
// question is still open.
func planTold() []step {
	return []step{bash("git log --oneline -1")}
}

// planWritten is the turn of the second answer: the session writes the whole plan, with what the person
// typed as a draft decision, commits it, reads it back, and submits it.
func planWritten() []step {
	plan := append([]string{"+++", "kind = \"plan\"", "id = \"harbor-v1\"", "title = \"Berths and sailings on the public pages\"",
		"goal = \"berths-on-the-web\"", "documents = [\"requirements:harbor\", \"milestone:berths-api\", \"milestone:berths-pages\"]", ""},
		sailingsDecision...)
	return []step{
		writeLines("plan/plan.md",
			append(plan, "+++", "", "The API serves the berths and their sailings first, and then the public pages show them.")...),
		writeLines("plan/requirements.md",
			"+++", "kind = \"requirements\"", "id = \"harbor\"", "title = \"What the berths pages must do\"", "plan = \"harbor-v1\"", "",
			"[[requirement]]", "id = \"HAR-1\"", "text = \"The API lists every berth with its state.\"", "",
			"[[requirement]]", "id = \"HAR-2\"", "text = \"The API lists the sailings of the next 7 days from a berth.\"", "",
			"[[requirement]]", "id = \"HAR-3\"", "text = \"The berths page lists every berth, and marks a berth closed for maintenance.\"", "",
			"[[requirement]]", "id = \"HAR-4\"", "text = \"Each berth shows its sailings under it.\"", "+++", "",
			"What visitors need from the public pages, and what the API serves them."),
		writeLines("plan/m-berths-api.md",
			"+++", "kind = \"milestone\"", "id = \"berths-api\"", "title = \"Berths in the API\"", "plan = \"harbor-v1\"", "priority = 1", "",
			"[[exit_criteria]]", "id = \"e1\"", "text = \"The API serves every berth and its sailings\"", "+++", "",
			"The API comes first, because the pages read everything from it."),
		writeLines("plan/m-berths-pages.md",
			"+++", "kind = \"milestone\"", "id = \"berths-pages\"", "title = \"Berths on the public pages\"", "plan = \"harbor-v1\"", "priority = 2",
			"depends_on = [\"berths-api\"]", "",
			"[[exit_criteria]]", "id = \"e1\"", "text = \"A visitor reads every berth and its sailings on one page\"", "+++", "",
			"The public pages show what the API serves."),
		writeLines("plan/c-berths-endpoint.md",
			"+++", "kind = \"contract\"", "id = \"berths-endpoint\"", "title = \"Berths endpoint\"", "plan = \"harbor-v1\"",
			"milestone = \"berths-api\"", "shared = true", "",
			"[[criteria]]", "id = \"c1\"", "text = \"GET /berths lists every berth with its state\"", "requirements = [\"HAR-1\"]", "",
			"[[work_orders]]", "id = \"serve-berths\"", "title = \"Serve the list of berths\"", "repo = \"harbor-api\"", "priority = 1",
			"size = \"medium\"", "+++", "",
			"One endpoint lists the berths, which the sailings and the pages build on."),
		writeLines("plan/c-sailings-endpoint.md",
			"+++", "kind = \"contract\"", "id = \"sailings-endpoint\"", "title = \"Sailings endpoint\"", "plan = \"harbor-v1\"",
			"milestone = \"berths-api\"", "uses = [\"berths-endpoint\"]", "",
			"[[criteria]]", "id = \"c1\"", "text = \"GET /berths/{id}/sailings lists the sailings of the next 7 days\"", "requirements = [\"HAR-2\"]", "",
			"[[work_orders]]", "id = \"serve-sailings\"", "title = \"Serve the sailings of each berth\"", "repo = \"harbor-api\"",
			"priority = 2", "size = \"small\"", "+++", "",
			"Each berth lists the sailings that leave from it in the next 7 days."),
		writeLines("plan/c-berths-page.md",
			"+++", "kind = \"contract\"", "id = \"berths-page\"", "title = \"Berths page\"", "plan = \"harbor-v1\"", "milestone = \"berths-pages\"", "",
			"[[criteria]]", "id = \"c1\"", "text = \"The page lists every berth and marks a berth closed for maintenance\"", "requirements = [\"HAR-3\"]", "",
			"[[criteria]]", "id = \"c2\"", "text = \"Each berth shows its sailings under it\"", "requirements = [\"HAR-4\"]", "",
			"[[work_orders]]", "id = \"build-berths-page\"", "title = \"Build the berths page\"", "repo = \"harbor-web\"", "priority = 1",
			"size = \"medium\"", "",
			"[[work_orders]]", "id = \"show-sailings\"", "title = \"Show the sailings under each berth\"", "repo = \"harbor-web\"",
			"priority = 2", "size = \"small\"", "depends_on = [\"build-berths-page\"]", "+++", "",
			"One public page lists the berths, each with its sailings under it."),
		commitAll("Plan the berths and their sailings"),
		bash("git log --oneline -1"),
		{"sleep_ms": planShown},
		submitted(),
	}
}

// adoptOpening is guide 4's first turn: it reads the planning repository and asks which branches in flight
// the plan keeps.
func adoptOpening() []step {
	return []step{
		bash("git log --oneline -3"),
		ask(questionKeepBranches, "Keep "+branchCancel+", and leave out "+branchMapView, "Keep both branches"),
	}
}

// adoptWritten is the turn the answer starts: the session onboards the requirements, marks what the code
// already does, continues the kept branch, leaves the other out, plans what remains, and submits it all.
func adoptWritten() []step {
	return []step{
		writeLines("docs/requirements.md",
			"+++", "kind = \"requirements\"", "id = \"moorings\"", "title = \"Requirements\"", "plan = \"moorings-v1\"", "",
			"[[requirement]]", "id = \"MOR-1\"", "text = \"A skipper books a mooring for a span of nights.\"", "status = \"met\"",
			"evidence = [\"moorings-api:internal/bookings/bookings.go\", \"moorings-api:internal/bookings/bookings_test.go\"]", "",
			"[[requirement]]", "id = \"MOR-2\"", "text = \"A skipper cancels a booking up to a day before arrival.\"", "",
			"[[requirement]]", "id = \"MOR-3\"", "text = \"A skipper gets a reminder the day before arrival.\"", "+++", "",
			"# Requirements", "",
			"- MOR-1: A skipper books a mooring for a span of nights.",
			"- MOR-2: A skipper cancels a booking up to a day before arrival.",
			"- MOR-3: A skipper gets a reminder the day before arrival."),
		writeLines("plan/goal.md",
			"+++", "kind = \"goal\"", "id = \"moorings-bookings\"", "title = \"Visiting boats book, cancel, and are reminded\"",
			"success_measures = [\"A skipper cancels a booking without calling the harbor office\"]", "+++", "",
			"Skippers book their moorings on the web today. They should cancel there too, and hear before they arrive."),
		writeLines("plan/plan.md",
			"+++", "kind = \"plan\"", "id = \"moorings-v1\"", "title = \"Moorings as it is, and what remains\"", "goal = \"moorings-bookings\"",
			"documents = [\"requirements:moorings\", \"milestone:bookings-today\", \"milestone:cancel-and-remind\"]", "",
			"[[left_out]]", "ref = \"branch:"+branchMapView+"\"", "reason = \"An experiment the person left out.\"", "+++", "",
			"Booking works today. Cancelling finishes the branch already in flight, and reminders are new."),
		writeLines("plan/m-bookings-today.md",
			"+++", "kind = \"milestone\"", "id = \"bookings-today\"", "title = \"Bookings as they are\"", "plan = \"moorings-v1\"",
			"priority = 0", "existing = true", "+++", "",
			"A skipper books a mooring on the web today."),
		writeLines("plan/m-cancel-and-remind.md",
			"+++", "kind = \"milestone\"", "id = \"cancel-and-remind\"", "title = \"Cancellations and reminders\"", "plan = \"moorings-v1\"",
			"priority = 1", "",
			"[[exit_criteria]]", "id = \"e1\"", "text = \"A skipper cancels a booking and is reminded before arrival\"", "+++", "",
			"What remains to build."),
		writeLines("plan/c-bookings.md",
			"+++", "kind = \"contract\"", "id = \"bookings\"", "title = \"Bookings\"", "plan = \"moorings-v1\"", "milestone = \"bookings-today\"",
			"shared = true", "existing = true", "",
			"[[criteria]]", "id = \"c1\"", "text = \"A skipper books a mooring for a span of nights\"", "requirements = [\"MOR-1\"]", "+++", "",
			"Bookings as the code holds them, which cancelling and reminders build on."),
		writeLines("plan/c-cancel-booking.md",
			"+++", "kind = \"contract\"", "id = \"cancel-booking\"", "title = \"Cancel a booking\"", "plan = \"moorings-v1\"",
			"milestone = \"cancel-and-remind\"", "uses = [\"bookings\"]", "",
			"[[criteria]]", "id = \"c1\"", "text = \"A skipper cancels a booking up to a day before arrival\"", "requirements = [\"MOR-2\"]", "",
			"[[work_orders]]", "id = \"finish-cancel\"", "title = \"Finish cancelling a booking\"", "repo = \"moorings-api\"", "priority = 1",
			"size = \"small\"", "continues = \""+branchCancel+"\"", "+++", "",
			"The branch in flight already frees a booked mooring. Finish it."),
		writeLines("plan/c-reminders.md",
			"+++", "kind = \"contract\"", "id = \"reminders\"", "title = \"Arrival reminders\"", "plan = \"moorings-v1\"",
			"milestone = \"cancel-and-remind\"", "uses = [\"bookings\"]", "",
			"[[criteria]]", "id = \"c1\"", "text = \"A skipper gets a reminder the day before arrival\"", "requirements = [\"MOR-3\"]", "",
			"[[work_orders]]", "id = \"remind-skippers\"", "title = \"Remind the skipper the day before arrival\"", "repo = \"moorings-api\"",
			"priority = 2", "size = \"medium\"", "+++", "",
			"A skipper hears from the harbor the day before arrival."),
		commitAll("Adopt Moorings"),
		submitted(),
	}
}
