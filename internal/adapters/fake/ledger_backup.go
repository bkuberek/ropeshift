package fake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/ports"
)

// LedgerBackup copies a list of marks, which stands for a ledger, to a file, one mark a line. It
// refuses the same paths the SQLite backup refuses, and never replaces a file either.
type LedgerBackup struct {
	mu    sync.Mutex
	marks []string
	paths []string
}

// NewLedgerBackup returns a backup of an empty ledger.
func NewLedgerBackup() *LedgerBackup { return &LedgerBackup{} }

// Mark records one change to the ledger the fake copies.
func (b *LedgerBackup) Mark(mark string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.marks = append(b.marks, mark)
}

// Paths lists where copies went, in order.
func (b *LedgerBackup) Paths() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.paths...)
}

// Backup writes the marks at path.
func (b *LedgerBackup) Backup(_ context.Context, path string) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !filepath.IsAbs(path) {
		return 0, fmt.Errorf("%w: %q is relative", ports.ErrBackupTarget, path)
	}
	body := strings.Join(b.marks, "\n") + "\n"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ports.ErrBackupTarget, err)
	}
	if _, err := file.WriteString(body); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	b.paths = append(b.paths, path)
	return int64(len(body)), nil
}

// ReadLedgerBackup returns the marks a copy holds.
func ReadLedgerBackup(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	body, found := strings.CutSuffix(string(raw), "\n")
	if !found {
		return nil, errors.New("a copy the fake did not write")
	}
	return strings.Split(body, "\n"), nil
}

var _ ports.LedgerBackup = (*LedgerBackup)(nil)
