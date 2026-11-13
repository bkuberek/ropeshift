//go:build chaos

package chaos

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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
// whole. A write is not atomic against SIGKILL, though: one that crosses a page
// boundary can be cut short, leaving a fragment. Each record therefore starts
// with a newline, so a fragment ends up on its own line, and ReadEffectLog skips
// it. A killed writer's lost record means the same as the writer having been
// killed just before it wrote.
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
	if _, err := l.file.Write(append([]byte{'\n'}, raw...)); err != nil {
		return err
	}
	return syscall.Fsync(int(l.file.Fd()))
}

// Close closes the log.
func (l *EffectLog) Close() error { return l.file.Close() }

// recordStart opens every record. Quotes inside a string value are escaped, so
// this text appears only where a record begins, even after a fragment on the same line.
const recordStart = `{"k":`

// ReadEffectLog parses the whole log. A fragment a killed writer left is skipped.
// A complete record that does not parse is an error, because the witness could
// not be trusted.
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
		// A fragment can share its line with the record written after it.
		for _, text := range splitRecords(scanner.Text()) {
			if strings.TrimSpace(text) == "" {
				continue
			}
			var r Record
			if err := json.Unmarshal([]byte(text), &r); err != nil {
				if truncated(text) {
					continue
				}
				return nil, fmt.Errorf("malformed record %q: %w", text, err)
			}
			out = append(out, r)
		}
	}
	return out, scanner.Err()
}

// splitRecords cuts a line before each record start.
func splitRecords(line string) []string {
	var parts []string
	for line != "" {
		i := strings.Index(line[1:], recordStart) + 1
		if i == 0 {
			return append(parts, line)
		}
		parts = append(parts, line[:i])
		line = line[i:]
	}
	return parts
}

// truncated reports whether text is the start of a JSON value that ends early.
func truncated(text string) bool {
	err := json.NewDecoder(bytes.NewReader([]byte(text))).Decode(new(json.RawMessage))
	return errors.Is(err, io.ErrUnexpectedEOF)
}
