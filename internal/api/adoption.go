package api

import (
	"context"
	"net/http"

	"ropeshift.com/ropeshift/internal/planning"
)

// AdoptionPlanPath is the route that plans an adoption. It is a project's route, and the installation answers
// it with no project too, for a project the adoption has yet to create.
const AdoptionPlanPath = "/v1/adoption/plan"

// AdoptionPlanRequest asks for the plan of an adoption of the clone at Root, on the machine of the worker the
// daemon runs (FR-ADOPT-12). It reads the layout, and records nothing.
type AdoptionPlanRequest struct {
	// Root is the top of the clone adopt runs in, as an absolute path.
	Root string `json:"root"`
	// Paths are absolute paths beside it, each a clone or a folder that holds clones.
	Paths []string `json:"paths,omitempty"`
	// Depth is how many levels below a folder are searched for clones. Zero searches one.
	Depth int `json:"depth,omitempty"`
	// Planning names the repository that holds the plan, and Design the design repository, each by its clone's
	// folder name.
	Planning string `json:"planning,omitempty"`
	Design   string `json:"design,omitempty"`
	// LeaveOut names the design repository's code trees a consolidation removes, relative to its top.
	LeaveOut []string `json:"leave_out,omitempty"`
	// NewProject says the project does not exist yet, so the plan reads no record of any project.
	NewProject bool `json:"new_project,omitempty"`
	// Practices asks that each repository the plan would register be read for its practices, as a dry run lists
	// them (FR-PRAC-6). They are no part of the plan's digest, and nothing is recorded.
	Practices bool `json:"practices,omitempty"`
}

// AdoptionPlanReply is what the plan route answers: the plan, flat as it always was, and the digest of each of its
// parts. A client that cannot compute the parts, as the web UI cannot, sends them back with the restructuring it
// chooses, so the worker that reads the layout again can name what differs. They are no part of the plan, and its
// digest does not hold them. A client of this module reads the plan alone.
//
// Guides are the practice table's words for each practice a finding of the plan names, by practice ID: the plan's
// results hold the findings, and a client that does not carry the table prints these.
type AdoptionPlanReply struct {
	planning.AdoptionPlan
	Parts  map[string]string        `json:"parts"`
	Guides map[string]PracticeGuide `json:"guides,omitempty"`
}

// AdoptionGuides collects the guide of each practice a finding of the plan's repositories names.
func AdoptionGuides(plan planning.AdoptionPlan) map[string]PracticeGuide {
	var guides map[string]PracticeGuide
	for _, repository := range plan.Repositories {
		if repository.Practices == nil {
			continue
		}
		for _, finding := range repository.Practices.Findings {
			if guides == nil {
				guides = map[string]PracticeGuide{}
			}
			guides[string(finding.Practice)] = guideOf(finding.Practice)
		}
	}
	return guides
}

// AdoptionPlan reads the plan of an adoption into the project, which only a member can do.
func (c *ProjectClient) AdoptionPlan(ctx context.Context, in AdoptionPlanRequest) (planning.AdoptionPlan, error) {
	var out planning.AdoptionPlan
	err := c.do(ctx, http.MethodPost, AdoptionPlanPath, in, &out)
	return out, err
}

// AdoptionPlan reads the plan of an adoption into a project that does not exist yet, which only a member can
// do. It sends NewProject, so no record of any project is read.
func (c *Client) AdoptionPlan(ctx context.Context, in AdoptionPlanRequest) (planning.AdoptionPlan, error) {
	var out planning.AdoptionPlan
	in.NewProject = true
	err := c.Do(ctx, http.MethodPost, AdoptionPlanPath, in, &out)
	return out, err
}
