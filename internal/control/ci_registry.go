package control

import (
	"context"
	"strings"
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
)

// ciRegistryLife is how long the map from a repository to its key's variable is kept without a project's revision
// having moved. A change of a repository record moves the revision of its project, which a heartbeat sees within a
// beat, so this only bounds what a change nobody saw can cost.
const ciRegistryLife = time.Minute

// ciRegistryBackoff is how long after a read that failed the registry answers with that failure and reads nothing.
const ciRegistryBackoff = 5 * time.Second

// CIKey is the name a repository is found by: its host, owner, and name, without regard to case, as GitHub names
// them.
func CIKey(host, owner, repo string) string {
	return strings.ToLower(host + "/" + owner + "/" + repo)
}

// CIRegistrations is every repository that every active project holds, by CIKey, and the revision of each of those
// projects at the moment its records were read.
type CIRegistrations struct {
	Repositories map[string][]CIRegistration
	Revisions    map[string]int64
}

// CIRegistry keeps the map from a repository to the variable that holds its webhook key in memory, so a delivery
// reads no store, and an unsigned flood cannot make the server read every project's. It is read again when a
// project's revision moves, which is how a changed repository record shows, and when it has grown old.
type CIRegistry struct {
	load func(context.Context) (CIRegistrations, error)
	now  func() time.Time

	// reading lets one read run at a time, and is taken before mu, which is never held across a read.
	reading sync.Mutex

	mu       sync.Mutex
	held     *CIRegistrations
	readAt   time.Time
	stale    bool
	failure  error
	failedAt time.Time
}

var _ CIRepositories = (*CIRegistry)(nil)

// NewCIRegistry returns a registry that reads what it holds with load.
func NewCIRegistry(load func(context.Context) (CIRegistrations, error), now func() time.Time) *CIRegistry {
	return &CIRegistry{load: load, now: now}
}

// CIRepository finds the registrations of a repository, from memory unless the registry is stale.
func (c *CIRegistry) CIRepository(ctx context.Context, host, owner, repo string) ([]CIRegistration, error) {
	held, err := c.current(ctx)
	if err != nil {
		return nil, err
	}
	return held.Repositories[CIKey(host, owner, repo)], nil
}

// fresh returns what is held when it can be used as it is, and the error of a read that failed a moment ago when the
// registry is still backing off from it. It holds mu.
func (c *CIRegistry) fresh() (*CIRegistrations, bool, error) {
	now := c.now()
	if c.held != nil && !c.stale && now.Sub(c.readAt) < ciRegistryLife {
		return c.held, true, nil
	}
	if c.failure != nil && now.Sub(c.failedAt) < ciRegistryBackoff {
		return nil, true, c.failure
	}
	return nil, false, nil
}

// current returns what the registry holds, read again when it must be. The read runs outside mu, which a heartbeat's
// Observe takes, and one read runs at a time: a delivery that waits for it takes its result, or the failure of it.
// A failed read holds nothing and is not tried again for ciRegistryBackoff, so a flood of deliveries cannot make the
// server read its stores at every one.
func (c *CIRegistry) current(ctx context.Context) (*CIRegistrations, error) {
	c.mu.Lock()
	held, ok, err := c.fresh()
	c.mu.Unlock()
	if ok {
		return held, err
	}
	c.reading.Lock()
	defer c.reading.Unlock()
	c.mu.Lock()
	held, ok, err = c.fresh()
	c.mu.Unlock()
	if ok {
		return held, err
	}
	read, err := c.load(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.failure, c.failedAt = err, c.now()
		return nil, err
	}
	c.held, c.readAt, c.stale, c.failure = &read, c.now(), false, nil
	return c.held, nil
}

// Observe takes the revisions a heartbeat read of every project, and the installation's, and makes the registry stale
// when a project moved, came, or went since its records were read. The installation's own revision is no project's.
func (c *CIRegistry) Observe(revisions map[string]int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held == nil || c.stale {
		return
	}
	seen := 0
	for project, rev := range revisions {
		if project == ledger.RevisionInstallation {
			continue
		}
		seen++
		if at, held := c.held.Revisions[project]; !held || at != rev {
			c.stale = true
			return
		}
	}
	if seen != len(c.held.Revisions) {
		c.stale = true
	}
}
