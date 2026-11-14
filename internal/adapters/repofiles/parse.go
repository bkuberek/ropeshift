// Package repofiles is the WorkSource on repository files: Markdown with TOML front
// matter, read at a committed ref (ADR-0013). It has no write capability.
package repofiles

import (
	"fmt"

	"github.com/BurntSushi/toml"

	"ropeshift.com/ropeshift/internal/planning"
)

// ParseFile decodes one repository file. A file with no front matter is
// planning.ErrNotOurs, and every other failure names what is wrong with the file.
func ParseFile(raw []byte) (planning.Document, error) {
	header, body, err := planning.SplitFrontMatter(raw)
	if err != nil {
		return planning.Document{}, err
	}
	var kind struct {
		Kind planning.FileKind `toml:"kind"`
	}
	if _, err := toml.Decode(string(header), &kind); err != nil {
		return planning.Document{}, fmt.Errorf("%w: %v", planning.ErrBadFile, err)
	}
	if err := planning.CheckKind(kind.Kind); err != nil {
		return planning.Document{}, err
	}
	doc := planning.Document{Kind: kind.Kind, Statement: string(body)}
	switch kind.Kind {
	case planning.FileGoal:
		doc.Goal, err = decode[planning.GoalFile](header)
	case planning.FilePlan:
		doc.Plan, err = decode[planning.PlanFile](header)
	case planning.FileRequirements:
		doc.Requirements, err = decode[planning.RequirementsFile](header)
	case planning.FileSpec:
		doc.Spec, err = decode[planning.SpecFile](header)
	case planning.FileArchitecture:
		doc.Architecture, err = decode[planning.ArchitectureFile](header)
	case planning.FileExperienceDesign:
		doc.Experience, err = decode[planning.ExperienceDesignFile](header)
	case planning.FileMilestone:
		doc.Milestone, err = decode[planning.MilestoneFile](header)
	case planning.FileRepositories:
		doc.Repositories, err = decodeManifest(header)
	default:
		doc.Contract, err = decode[planning.ContractFile](header)
	}
	if err != nil {
		return planning.Document{}, err
	}
	return doc, nil
}

// decode reads one kind's front matter and applies that kind's rules.
func decode[F interface{ Normalize() (F, error) }](header []byte) (*F, error) {
	var file F
	if _, err := toml.Decode(string(header), &file); err != nil {
		return nil, fmt.Errorf("%w: %v", planning.ErrBadFile, err)
	}
	normal, err := file.Normalize()
	if err != nil {
		return nil, err
	}
	return &normal, nil
}

// decodeManifest reads a repository manifest, and keeps every key the format does not have, so intake can
// say that each has no effect: a manifest that tries to set a member's setting changes nothing.
func decodeManifest(header []byte) (*planning.RepositoriesFile, error) {
	var file planning.RepositoriesFile
	meta, err := toml.Decode(string(header), &file)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", planning.ErrBadFile, err)
	}
	for _, key := range meta.Undecoded() {
		file.Ignored = append(file.Ignored, key.String())
	}
	normal, err := file.Normalize()
	if err != nil {
		return nil, err
	}
	return &normal, nil
}
