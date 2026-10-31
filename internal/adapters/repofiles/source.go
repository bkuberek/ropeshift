package repofiles

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"ropeshift.dev/ropeshift/internal/adapters/gitcli"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
)

// Adapter is the first part of every external key this source returns.
const Adapter = "repofiles"

// Config names one repository and what to read in it.
type Config struct {
	// Repository is a local working copy or bare repository.
	Repository string
	// Ref is what intake reads. Empty means the default branch.
	Ref string
	// Directory limits intake to one tree. Empty means the whole repository.
	Directory string
	// Env replaces the environment of git commands.
	Env []string
}

// Source reads contract files at a committed ref. An uncommitted edit is not intake,
// so a contract version always names a commit.
type Source struct {
	config Config
	git    gitcli.Git

	mu     sync.Mutex
	health ports.SourceHealth
}

// New returns a source on one repository.
func New(config Config) *Source {
	return &Source{
		config: config,
		git:    gitcli.Git{Dir: config.Repository, Env: config.Env},
		health: ports.SourceHealth{State: planning.HealthOK},
	}
}

// Describe names the repository by its first commit, because a path changes with
// the machine, a remote may not exist, and neither identifies the history.
func (s *Source) Describe(ctx context.Context) (ports.SourceDescription, error) {
	ref, err := s.ref(ctx)
	if err != nil {
		return ports.SourceDescription{}, err
	}
	roots, err := s.git.Text(ctx, "rev-list", "--max-parents=0", ref)
	if err != nil {
		return ports.SourceDescription{}, err
	}
	all := strings.Fields(roots)
	if len(all) == 0 {
		return ports.SourceDescription{}, fmt.Errorf("%s has no commits", ref)
	}
	slices.Sort(all)
	return ports.SourceDescription{
		Adapter:     Adapter,
		Instance:    all[0],
		APIVersions: []string{"adr-0013"},
		BudgetModel: "none",
	}, nil
}

// Fields returns the keys of a contract file.
func (s *Source) Fields(context.Context, ports.ScopeRef) ([]ports.FieldSchema, error) {
	return []ports.FieldSchema{
		{Name: "kind", Type: "string", Required: true},
		{Name: "id", Type: "string", Required: true},
		{Name: "title", Type: "string", Required: true},
		{Name: "plan", Type: "string"},
		{Name: "requirements", Type: "array"},
		{Name: "decisions", Type: "array"},
		{Name: "criteria", Type: "array"},
		{Name: "work_orders", Type: "array"},
	}, nil
}

// Poll reads the ref. A fetch runs only when a remote exists, and a failed one is a
// health state, so intake still reads what the machine has. The cursor is the
// commit, so an unchanged ref reads nothing.
func (s *Source) Poll(ctx context.Context, cursor ports.Cursor) (ports.ChangeBatch, error) {
	s.fetch(ctx)
	ref, err := s.ref(ctx)
	if err != nil {
		return ports.ChangeBatch{}, err
	}
	commit, err := s.git.Commit(ctx, ref)
	if err != nil {
		return ports.ChangeBatch{}, err
	}
	if ports.Cursor(commit) == cursor {
		return ports.ChangeBatch{Cursor: cursor}, nil
	}
	batch := ports.ChangeBatch{Cursor: ports.Cursor(commit)}
	blobs, err := s.tree(ctx, commit)
	if err != nil {
		return ports.ChangeBatch{}, err
	}
	description, err := s.Describe(ctx)
	if err != nil {
		return ports.ChangeBatch{}, err
	}
	seen := map[string]string{}
	for _, blob := range blobs {
		raw, err := s.git.Output(ctx, "cat-file", "blob", blob.hash)
		if err != nil {
			return ports.ChangeBatch{}, err
		}
		doc, err := ParseFile(raw)
		switch {
		case errors.Is(err, planning.ErrNotOurs):
			continue
		case err != nil:
			batch.Problems = append(batch.Problems, planning.SourceProblem{Path: blob.path, Detail: err.Error()})
			continue
		case doc.Contract == nil:
			continue
		}
		if first, repeated := seen[doc.Contract.ID]; repeated {
			batch.Problems = append(batch.Problems, planning.SourceProblem{
				Path:   blob.path,
				Detail: fmt.Sprintf("%v: contract %s is already in %s", planning.ErrBadFile, doc.Contract.ID, first),
			})
			continue
		}
		seen[doc.Contract.ID] = blob.path
		batch.Items = append(batch.Items, itemOf(description, doc, blob, commit))
	}
	return batch, nil
}

func itemOf(description ports.SourceDescription, doc planning.Document, blob treeBlob, commit string) ports.WorkItem {
	return ports.WorkItem{
		Key:       ledger.ExternalKey{Adapter: description.Adapter, Instance: description.Instance, NativeID: doc.Contract.ID},
		Digest:    planning.DigestGitBlob + blob.hash,
		Source:    blob.path + "@" + commit,
		Path:      blob.path,
		Contract:  *doc.Contract,
		Statement: doc.Statement,
	}
}

// Read returns the contract with this native ID at the ref as it is now.
func (s *Source) Read(ctx context.Context, key ledger.ExternalKey) (ports.WorkItem, error) {
	batch, err := s.Poll(ctx, "")
	if err != nil {
		return ports.WorkItem{}, err
	}
	for _, item := range batch.Items {
		if item.Key == key {
			return item, nil
		}
	}
	return ports.WorkItem{}, fmt.Errorf("%s: %w", key, ports.ErrNotFound)
}

// VerifyDelivery reports that repository files take no webhooks: polling must work
// alone.
func (s *Source) VerifyDelivery(context.Context, ports.InboundDelivery) (ports.ChangeHint, error) {
	return ports.ChangeHint{}, ports.ErrUnsupported
}

// Apply reports `unsupported`, because the ledger alone records execution.
func (s *Source) Apply(context.Context, ports.OutboxItem) (ports.WriteResult, error) {
	return ports.WriteResult{Status: ports.WriteUnsupported, Detail: "repository files have no write capability"}, nil
}

// Health reports the last fetch. A file that cannot be read is a problem in the
// batch and not a health state of the source.
func (s *Source) Health(context.Context) (ports.SourceHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health, nil
}

func (s *Source) setHealth(health ports.SourceHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health = health
}

func (s *Source) fetch(ctx context.Context) {
	remote, err := s.remote(ctx)
	if err != nil || remote == "" {
		s.setHealth(ports.SourceHealth{State: planning.HealthOK})
		return
	}
	if _, err := s.git.Output(ctx, "fetch", "--quiet", remote); err != nil {
		s.setHealth(ports.SourceHealth{State: planning.HealthFetchFailed, Detail: err.Error()})
		return
	}
	s.setHealth(ports.SourceHealth{State: planning.HealthOK})
}

// remote prefers origin, and any other remote will do.
func (s *Source) remote(ctx context.Context) (string, error) {
	out, err := s.git.Text(ctx, "remote")
	if err != nil {
		return "", err
	}
	names := strings.Fields(out)
	if len(names) == 0 {
		return "", nil
	}
	if slices.Contains(names, "origin") {
		return "origin", nil
	}
	return names[0], nil
}

// ref resolves what to read: the configured ref, or else the default branch, on the
// remote when there is one.
func (s *Source) ref(ctx context.Context) (string, error) {
	if s.config.Ref != "" {
		return s.config.Ref, nil
	}
	remote, err := s.remote(ctx)
	if err != nil {
		return "", err
	}
	if remote != "" {
		if head, err := s.git.Text(ctx, "symbolic-ref", "--quiet", "refs/remotes/"+remote+"/HEAD"); err == nil && head != "" {
			return head, nil
		}
	}
	var candidates []string
	if remote != "" {
		candidates = append(candidates, "refs/remotes/"+remote+"/main", "refs/remotes/"+remote+"/master")
	}
	candidates = append(candidates, "refs/heads/main", "refs/heads/master")
	for _, candidate := range candidates {
		if _, err := s.git.Commit(ctx, candidate); err == nil {
			return candidate, nil
		}
	}
	return s.git.Text(ctx, "symbolic-ref", "--quiet", "HEAD")
}

type treeBlob struct {
	hash string
	path string
}

// tree lists the Markdown blobs of a commit. The hash in the listing is the digest
// of the file as written.
func (s *Source) tree(ctx context.Context, commit string) ([]treeBlob, error) {
	args := []string{"ls-tree", "-r", "-z", "--full-tree", commit}
	if dir := strings.Trim(s.config.Directory, "/"); dir != "" && dir != "." {
		args = append(args, "--", dir)
	}
	out, err := s.git.Output(ctx, args...)
	if err != nil {
		return nil, err
	}
	var blobs []treeBlob
	for entry := range strings.SplitSeq(string(out), "\x00") {
		meta, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" || path.Ext(name) != ".md" {
			continue
		}
		blobs = append(blobs, treeBlob{hash: fields[2], path: name})
	}
	return blobs, nil
}

var _ ports.WorkSource = (*Source)(nil)
