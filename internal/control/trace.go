package control

import (
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// planRequirements reads the requirement keys a plan's requirements documents hold at their newest
// versions, and says whether the plan has such a document at all. A plan with none holds its
// criteria to nothing (FR-DOC-5).
func planRequirements(planned ports.PlanningReader, plan string) (map[string]bool, bool, error) {
	documents, err := planned.PlanDocuments(plan)
	if err != nil {
		return nil, false, err
	}
	known := map[string]bool{}
	traced := false
	for _, document := range documents {
		if document.Kind != planning.FileRequirements {
			continue
		}
		traced = true
		latest, err := planned.LatestDocumentVersion(document.ID)
		if err != nil {
			return nil, false, err
		}
		var file planning.RequirementsFile
		if err := latest.Decode(&file); err != nil {
			return nil, false, err
		}
		for _, spec := range file.Requirements {
			known[spec.ID] = true
		}
	}
	return known, traced, nil
}
