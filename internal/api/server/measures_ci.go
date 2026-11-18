package server

import (
	"cmp"
	"net/http"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ports"
)

// readCIDurations reads what each repository's CI takes, from the CI records the control plane keeps for it
// (FR-SPEND-21), by the repository's own history keys.
func (h *handler) readCIDurations(w http.ResponseWriter, r *http.Request) {
	out := api.CIDurations{Repositories: []api.CIDuration{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		repositories, err := tx.Repositories().Repositories()
		if err != nil {
			return err
		}
		for _, repository := range repositories {
			settings := repository.CI.Effective()
			records, err := tx.Forge().CIRecords(repository.ID, int(settings.HistoryWindow))
			if err != nil {
				return err
			}
			stats := settings.Stats(records)
			none := settings.NoHistory()
			row := api.CIDuration{
				Repository: repository.ID, Name: repository.Name, Records: int64(stats.Records), MinRecords: settings.HistoryMinRecords, History: stats.Has,
				NoHistoryP50Ms: none.P50.Milliseconds(), NoHistoryP85Ms: none.P85.Milliseconds(),
			}
			if stats.Has {
				row.P50Ms, row.P85Ms = stats.Duration.P50.Milliseconds(), stats.Duration.P85.Milliseconds()
				row.QueueP50Ms, row.QueueP85Ms = stats.Queue.P50.Milliseconds(), stats.Queue.P85.Milliseconds()
			}
			out.Repositories = append(out.Repositories, row)
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	slices.SortStableFunc(out.Repositories, func(a, b api.CIDuration) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Repository, b.Repository))
	})
	reply(w, http.StatusOK, out)
}
