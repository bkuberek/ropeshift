package fake

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ForgeAccounts is a person's forge account held in memory: what the sign-in is, who its owners are, and which
// repositories exist. A test sets the fields before a worker reads them.
type ForgeAccounts struct {
	mu sync.Mutex
	// SignIn is what the sign-in reads as. NewForgeAccounts signs in as test, on https, with the repo scope.
	SignIn ports.ForgeAccount
	// Orgs are the organizations the account belongs to. OrgsScopeMissing makes listing them fail for a missing scope.
	Orgs             []string
	OrgsScopeMissing bool
	// CreateError is what creating a repository fails with.
	CreateError error
	existing    map[string]bool
	created     []Created
}

// Created is one repository the fake created.
type Created struct {
	Owner  string
	Name   string
	Public bool
}

// NewForgeAccounts returns an account signed in as test, on https, with the repo scope.
func NewForgeAccounts() *ForgeAccounts {
	return &ForgeAccounts{
		SignIn:   ports.ForgeAccount{State: ledger.ForgeAccountOK, Binary: "/usr/bin/gh", Login: "test", GitProtocol: "https", Scopes: []string{"repo", "read:org"}},
		existing: map[string]bool{},
	}
}

// Exist makes a repository exist already.
func (f *ForgeAccounts) Exist(owner, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.existing[owner+"/"+name] = true
}

// Repositories lists what the fake created, in order.
func (f *ForgeAccounts) Repositories() []Created {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.created)
}

// Account reads the sign-in.
func (f *ForgeAccounts) Account(_ context.Context, _ string) (ports.ForgeAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.SignIn, nil
}

// Owners lists the organizations.
func (f *ForgeAccounts) Owners(_ context.Context, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.OrgsScopeMissing {
		return nil, fmt.Errorf("listing organizations: %w", ports.ErrForgeScope)
	}
	return slices.Sorted(slices.Values(f.Orgs)), nil
}

// Exists says whether a repository exists.
func (f *ForgeAccounts) Exists(_ context.Context, _, owner, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.existing[owner+"/"+name], nil
}

// Create records a repository.
func (f *ForgeAccounts) Create(_ context.Context, _ string, repository ports.CreateRepository) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateError != nil {
		return f.CreateError
	}
	f.existing[repository.Owner+"/"+repository.Name] = true
	f.created = append(f.created, Created{Owner: repository.Owner, Name: repository.Name, Public: repository.Public})
	return nil
}

var _ ports.ForgeAccounts = (*ForgeAccounts)(nil)
