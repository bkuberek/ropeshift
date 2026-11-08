//go:build chaos

package chaos

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"syscall"
)

// The kinds of log record.
const (
	Attempted = "attempted"
	Completed = "completed"
	Rejected  = "rejected"
)

// Record is one line of the independent log.
type Record struct {
	Kind      string `json:"k"`
	Op        string `json:"op,omitempty"`
	WorkOrder string `json:"wo,omitempty"`
	Key       string `json:"key,omitempty"`
	Fence     int64  `json:"fence,omitempty"`
	Rev       int64  `json:"rev,omitempty"`
	Actor     string `json:"actor,omitempty"`
	Note      string `json:"note,omitempty"`
}

// EffectLog is the witness: a child writes it, the database never sees it, and the
// parent checks one against the other. Each record is one write on an O_APPEND
// descriptor followed by fsync, so records from several processes interleave
// whole and survive SIGKILL of any of them.
type EffectLog struct {
	file *os.File
}

// OpenEffectLog opens or creates the log.
func OpenEffectLog(path string) (*EffectLog, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &EffectLog{file: file}, nil
}

// Append writes one record. It calls fsync directly, because on macOS File.Sync
// flushes the drive cache, which guards against a power loss this harness does
// not simulate.
func (l *EffectLog) Append(r Record) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := l.file.Write(append(raw, '\n')); err != nil {
		return err
	}
	return syscall.Fsync(int(l.file.Fd()))
}

// Close closes the log.
func (l *EffectLog) Close() error { return l.file.Close() }

// ReadEffectLog parses the whole log. A line that does not parse is an error,
// because a torn record would mean the witness cannot be trusted.
func ReadEffectLog(path string) ([]Record, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []Record
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var r Record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("torn record %q: %w", scanner.Text(), err)
		}
		out = append(out, r)
	}
	return out, scanner.Err()
}
