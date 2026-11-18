package fake

import (
	"context"
	"sync"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
)

// Practices is the practices check's three ports held in memory. A test sets what a checkout holds, what a scan
// finds, and what a forge says, each by the directory or repository it is asked about, or a failure for a read.
type Practices struct {
	mu        sync.Mutex
	snapshots map[string]practices.Snapshot
	scans     map[string]ports.SecretScan
	facts     map[string]practices.ForgeFacts
	factsErrs map[string]error
	// Fail is what every read returns while it is set.
	Fail error
	// ScanCalls lists the arguments of each scan: the directory, the tip, and the commit it started from.
	ScanCalls [][3]string
	// SettingsCalls lists the arguments of each settings read: the repository's binding and the branch.
	SettingsCalls [][2]string
}

var (
	_ ports.Practices         = (*Practices)(nil)
	_ ports.PracticeSnapshots = (*Practices)(nil)
	_ ports.SecretScans       = (*Practices)(nil)
	_ ports.ForgeSettings     = (*Practices)(nil)
)

// SetSnapshot sets what the checkout in a directory holds.
func (p *Practices) SetSnapshot(dir string, snapshot practices.Snapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.snapshots == nil {
		p.snapshots = map[string]practices.Snapshot{}
	}
	p.snapshots[dir] = snapshot
}

// SetScan sets what a scan of the checkout in a directory finds.
func (p *Practices) SetScan(dir string, scan ports.SecretScan) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.scans == nil {
		p.scans = map[string]ports.SecretScan{}
	}
	p.scans[dir] = scan
}

// SetSettings sets what the forge says of a repository, by its binding's text.
func (p *Practices) SetSettings(binding string, facts practices.ForgeFacts) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.facts == nil {
		p.facts = map[string]practices.ForgeFacts{}
	}
	p.facts[binding] = facts
}

// SetSettingsError makes the settings read of a repository, by its binding's text, return an error with the facts set
// for it, as the read does for a rate limit. An error of nil clears it.
func (p *Practices) SetSettingsError(binding string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.factsErrs == nil {
		p.factsErrs = map[string]error{}
	}
	p.factsErrs[binding] = err
}

// Snapshot returns what the directory holds, and an empty snapshot of the commit for a directory with none set.
func (p *Practices) Snapshot(_ context.Context, dir, commit string) (practices.Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Fail != nil {
		return practices.Snapshot{}, p.Fail
	}
	if snapshot, ok := p.snapshots[dir]; ok {
		return snapshot, nil
	}
	return practices.Snapshot{Commit: commit}, nil
}

// Scan returns what a scan of the directory finds, and nothing for a directory with none set.
func (p *Practices) Scan(_ context.Context, dir, tip, from string) (ports.SecretScan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ScanCalls = append(p.ScanCalls, [3]string{dir, tip, from})
	if p.Fail != nil {
		return ports.SecretScan{}, p.Fail
	}
	return p.scans[dir], nil
}

// Settings returns what the forge says of the repository, and zero facts for a repository with none set, with the
// error set for the repository.
func (p *Practices) Settings(_ context.Context, repo ports.ForgeRepo, branch string) (practices.ForgeFacts, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	binding := repo.Binding.String()
	p.SettingsCalls = append(p.SettingsCalls, [2]string{binding, branch})
	if p.Fail != nil {
		return practices.ForgeFacts{}, p.Fail
	}
	return p.facts[binding], p.factsErrs[binding]
}
