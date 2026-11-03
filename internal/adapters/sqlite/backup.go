package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	msqlite "modernc.org/sqlite"

	"ropeshift.dev/ropeshift/internal/ports"
)

// LedgerBackup copies the ledger in File through SQLite's online backup API, which reads one
// consistent snapshot while the daemon goes on writing.
type LedgerBackup struct {
	File string
}

// backuper is the driver connection's side of the online backup API.
type backuper interface {
	NewBackup(dstURI string) (*msqlite.Backup, error)
}

// Backup writes a copy of the ledger at path. The copy goes to a hidden file beside the path, and
// takes the path only once it is whole, so a failed copy leaves nothing a person could mistake for
// a backup, and an existing file is never replaced.
func (b LedgerBackup) Backup(ctx context.Context, path string) (int64, error) {
	if err := checkBackupTarget(path); err != nil {
		return 0, err
	}
	partial, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".partial-*")
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ports.ErrBackupTarget, err)
	}
	name := partial.Name()
	defer os.Remove(name)
	if err := partial.Close(); err != nil {
		return 0, err
	}
	if err := copyOnline(ctx, b.File, name); err != nil {
		return 0, err
	}
	if err := settle(ctx, name); err != nil {
		return 0, err
	}
	if err := takePath(name, path); err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// checkBackupTarget refuses a path that cannot take a new file.
func checkBackupTarget(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: %q is relative", ports.ErrBackupTarget, path)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%w: %s exists", ports.ErrBackupTarget, path)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		return fmt.Errorf("%w: no directory %s", ports.ErrBackupTarget, filepath.Dir(path))
	}
	return nil
}

// copyOnline runs the backup in one step. Under WAL a reader holds its snapshot without stopping a
// writer, so the daemon never waits on a backup.
func copyOnline(ctx context.Context, source, target string) error {
	db, err := OpenDB(source)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(driverConn any) error {
		raw, ok := driverConn.(backuper)
		if !ok {
			return errors.New("this SQLite driver has no online backup")
		}
		backup, err := raw.NewBackup(uri(target))
		if err != nil {
			return err
		}
		for {
			more, err := backup.Step(-1)
			if err != nil {
				_ = backup.Finish()
				return err
			}
			if !more {
				return backup.Finish()
			}
		}
	})
}

// settle makes the copy one file that needs nothing beside it, and puts it on the disk. A copy of a
// ledger in WAL mode would otherwise want a write-ahead log next to it.
func settle(ctx context.Context, name string) error {
	db, err := sql.Open("sqlite", uri(name))
	if err != nil {
		return err
	}
	var mode string
	err = db.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	file, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = file.Sync()
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// takePath gives the whole copy its name, and never over a file. A hard link fails on a name that
// exists. A file system with no hard links gets a check and a rename instead.
func takePath(name, path string) error {
	err := os.Link(name, path)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %s exists", ports.ErrBackupTarget, path)
	}
	if err != nil {
		if _, statErr := os.Lstat(path); statErr == nil {
			return fmt.Errorf("%w: %s exists", ports.ErrBackupTarget, path)
		}
		if err := os.Rename(name, path); err != nil {
			return err
		}
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// uri names a file for SQLite, which reads a file name as a URI: a question mark, a hash, or a
// percent sign in a directory's name would otherwise end the path or start an escape.
func uri(file string) string {
	return "file:" + strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(file)
}

var _ ports.LedgerBackup = LedgerBackup{}
