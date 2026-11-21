package entry

import (
	"context"
	"time"

	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// outsideReadTimeout bounds one project's read of its clones on the tick.
const outsideReadTimeout = 2 * time.Minute

// outsideEditsTick reads the clones of every active project, each read within timeout of its own, so a lane whose
// branch changed a file a person changed too is a conflict in Needs you though nobody opens Repositories (FR-UI-14),
// and a clone git never answers holds back no other project. A server that reads no clone has no tick.
func outsideEditsTick(config server.Config, projects ports.Projects, every, timeout time.Duration) control.Tick {
	return control.Tick{Name: "outside-edits", Every: every, Run: eachProject(projects, func(ctx context.Context, p ledger.Project, store ports.Store) error {
		bounded, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		_, err := config.OutsideEdits.Read(bounded, store, p.ID, p.Name)
		return err
	})}
}
