// Package repofiles is the WorkSource on repository files: Markdown with TOML front
// matter, read at a committed ref (ADR-0013). It has no write capability.
package repofiles

import (
	"fmt"

	"github.com/BurntSushi/toml"

	"ropeshift.dev/ropeshift/internal/planning"
)

// ParseFile decodes one repository file. A file with no front matter is
// planning.ErrNotOurs, and every other failure names what is wrong with the file.
func ParseFile(raw []byte) (planning.Document, error) {
	header, body, err := planning.SplitFrontMatter(raw)
	if err != nil {
		return planning.Document{}, err
	}
	var file planning.ContractFile
	if _, err := toml.Decode(string(header), &file); err != nil {
		return planning.Document{}, fmt.Errorf("%w: %v", planning.ErrBadFile, err)
	}
	if err := planning.CheckKind(file.Kind); err != nil {
		return planning.Document{}, err
	}
	if file.Kind != planning.FileContract {
		return planning.Document{Kind: file.Kind}, nil
	}
	file, err = file.Normalize()
	if err != nil {
		return planning.Document{}, err
	}
	return planning.Document{Kind: file.Kind, Contract: &file, Statement: string(body)}, nil
}
