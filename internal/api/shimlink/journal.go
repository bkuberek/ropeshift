package shimlink

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

const (
	segmentPrefix = "journal-"
	segmentSuffix = ".log"
	// DefaultCap bounds one generation's journal on disk.
	DefaultCap = 256 << 20
	// segmentsPerCap sets a segment's size, so rotation frees a fraction of the cap.
	segmentsPerCap = 4
)

// Journal is the append-only log of one generation, in segments named by their first
// sequence number. Rotation removes the oldest segment past the cap, so an absent reader
// costs bounded disk, and a reader that falls that far behind is told what it lost.
type Journal struct {
	dir     string
	cap     int64
	segment int64

	mu      sync.Mutex
	file    *os.File
	size    int64
	total   int64
	sizes   map[string]int64
	next    int64
	changed chan struct{}
	closed  bool
}

// OpenJournal creates the journal of a generation that is starting. A cap of zero takes
// the default.
func OpenJournal(dir string, capBytes int64) (*Journal, error) {
	if capBytes <= 0 {
		capBytes = DefaultCap
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	j := &Journal{
		dir: dir, cap: capBytes, segment: max(capBytes/segmentsPerCap, 1), sizes: map[string]int64{},
		next: 1, changed: make(chan struct{}),
	}
	if err := j.rotate(); err != nil {
		return nil, err
	}
	return j, nil
}

func segmentName(first int64) string {
	return fmt.Sprintf("%s%020d%s", segmentPrefix, first, segmentSuffix)
}

// rotate starts a new segment at the next sequence number, and removes the oldest while
// the rest passes the cap. An open reader keeps the file it holds.
func (j *Journal) rotate() error {
	if j.file != nil {
		if err := j.file.Close(); err != nil {
			return err
		}
	}
	name := segmentName(j.next)
	file, err := os.OpenFile(filepath.Join(j.dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	j.file, j.size = file, 0
	j.sizes[name] = 0
	for j.total > j.cap-j.segment {
		names := slices.Sorted(maps.Keys(j.sizes))
		if len(names) < 2 {
			break
		}
		oldest := names[0]
		if err := os.Remove(filepath.Join(j.dir, oldest)); err != nil {
			return err
		}
		j.total -= j.sizes[oldest]
		delete(j.sizes, oldest)
	}
	return nil
}

func segmentFirst(name string) (int64, error) {
	var first int64
	text := strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), segmentSuffix)
	if _, err := fmt.Sscanf(text, "%d", &first); err != nil {
		return 0, fmt.Errorf("segment %s: %w", name, err)
	}
	return first, nil
}

// Append assigns the next sequence number and writes one record in one write, so a kill
// can tear only the last line, and a reader skips that.
func (j *Journal) Append(record Record) (Record, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return Record{}, os.ErrClosed
	}
	record.Seq = j.next
	raw, err := json.Marshal(record)
	if err != nil {
		return Record{}, err
	}
	raw = append(raw, '\n')
	if j.size+int64(len(raw)) > j.segment && j.size > 0 {
		if err := j.rotate(); err != nil {
			return Record{}, err
		}
	}
	if _, err := j.file.Write(raw); err != nil {
		return Record{}, err
	}
	j.next++
	j.size += int64(len(raw))
	j.total += int64(len(raw))
	j.sizes[filepath.Base(j.file.Name())] = j.size
	close(j.changed)
	j.changed = make(chan struct{})
	return record, nil
}

// Close ends the journal. A tail reads what is there and stops.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	close(j.changed)
	return j.file.Close()
}

// Tail reads records after a sequence number, from disk, whether they are old or new.
type Tail struct {
	journal *Journal
	dir     string
	next    int64
	file    *os.File
	reader  *bufio.Reader
}

// Tail returns a reader of the records after since.
func (j *Journal) Tail(since int64) *Tail {
	return &Tail{journal: j, dir: j.dir, next: since + 1}
}

// ReadJournal returns the records after since from a journal no shim serves, which is
// how a worker reads a session that ended while it was away.
func ReadJournal(dir string, since int64) ([]Record, error) {
	tail := &Tail{dir: dir, next: since + 1}
	defer tail.Close()
	var out []Record
	for {
		record, err := tail.step()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
}

// Ended reports an exit on record, from the last segment, for a worker no shim answered.
func Ended(dir string) (bool, error) {
	files, err := segments(dir)
	if err != nil || len(files) == 0 {
		return false, err
	}
	first, err := segmentFirst(filepath.Base(files[len(files)-1]))
	if err != nil {
		return false, err
	}
	records, err := ReadJournal(dir, first-1)
	if err != nil {
		return false, err
	}
	return len(records) != 0 && records[len(records)-1].Exit != nil, nil
}

// Close releases the file a tail holds.
func (t *Tail) Close() {
	if t.file != nil {
		_ = t.file.Close()
		t.file, t.reader = nil, nil
	}
}

// Next returns the next record, and waits for one past the end. It returns io.EOF once a
// closed journal has nothing left.
func (t *Tail) Next(ctx context.Context) (Record, error) {
	for {
		var wait <-chan struct{}
		t.journal.mu.Lock()
		closed := t.journal.closed
		if !closed {
			wait = t.journal.changed
		}
		t.journal.mu.Unlock()
		record, err := t.step()
		if !errors.Is(err, io.EOF) {
			return record, err
		}
		if closed {
			return Record{}, io.EOF
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return Record{}, ctx.Err()
		}
	}
}

// segments lists the journal's files in sequence order.
func segments(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, segmentPrefix+"*"+segmentSuffix))
	if err != nil {
		return nil, err
	}
	slices.Sort(matches)
	return matches, nil
}

// step reads one record, or io.EOF at the end of what is on disk now.
func (t *Tail) step() (Record, error) {
	for {
		if t.file == nil {
			gap, err := t.open()
			if err != nil {
				return Record{}, err
			}
			if gap != 0 {
				return Record{Gap: gap}, nil
			}
		}
		line, err := t.reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			// A partial line is a write in flight or torn by a kill, and neither is a
			// record yet. The next read starts from this line's beginning.
			if len(line) != 0 {
				if _, err := t.file.Seek(-int64(len(line)), io.SeekCurrent); err != nil {
					return Record{}, err
				}
				t.reader.Reset(t.file)
			}
			moved, err := t.advance()
			if err != nil {
				return Record{}, err
			}
			if moved {
				continue
			}
			return Record{}, io.EOF
		}
		if err != nil {
			return Record{}, err
		}
		var record Record
		if err := json.Unmarshal(line, &record); err != nil {
			return Record{}, fmt.Errorf("journal: %w", err)
		}
		if record.Seq < t.next {
			continue
		}
		t.next = record.Seq + 1
		return record, nil
	}
}

// open finds the segment that holds the next record. It reports a gap when rotation took
// that record, so a reader knows what it lost and goes on from what is left.
func (t *Tail) open() (int64, error) {
	files, err := segments(t.dir)
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return 0, io.EOF
	}
	first, err := segmentFirst(filepath.Base(files[0]))
	if err != nil {
		return 0, err
	}
	var gap int64
	if t.next < first {
		gap = first - 1
		t.next = first
	}
	chosen := files[0]
	for _, file := range files[1:] {
		start, err := segmentFirst(filepath.Base(file))
		if err != nil {
			return 0, err
		}
		if start > t.next {
			break
		}
		chosen = file
	}
	file, err := os.Open(chosen)
	if errors.Is(err, os.ErrNotExist) {
		return t.open()
	}
	if err != nil {
		return 0, err
	}
	t.file, t.reader = file, bufio.NewReaderSize(file, 1<<16)
	return gap, nil
}

// advance moves on once a later segment exists, because the journal only ever writes to
// the last one. Rotation may have taken what lay between, and open reports that.
func (t *Tail) advance() (bool, error) {
	files, err := segments(t.dir)
	if err != nil {
		return false, err
	}
	current := t.file.Name()
	if slices.ContainsFunc(files, func(file string) bool { return file > current }) {
		t.Close()
		return true, nil
	}
	return false, nil
}
