package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// procRoot is where this machine lists its processes.
const procRoot = "/proc"

// controllingTerminal is the path of a descriptor a process opened to reach its own controlling terminal, which may
// be the terminal a step ran on, and which a process cannot be told from another's once the step's session ended.
const controllingTerminal = "/dev/tty"

// terminalHolders names every process that holds the terminal whose slave side is at slave open, as a line each: the
// process's ID, its command line, the descriptors it holds the terminal on, and every other descriptor it holds, so
// a process that kept what it was started with is seen with where that came from. A descriptor names the terminal
// by its path when the process was given it. One that a process opened as /dev/tty is listed too, marked, because it
// may be this terminal's, and a process cannot be told from another's once the step's session ended. A process of
// another user, whose descriptors this user may not read, is only counted, with its owners, as it holds the terminal
// only if a privileged program handed it on: `docker exec -u <owner>` reads such a process in a container. It reads
// only /proc, so on a machine with none it names nothing but that.
func terminalHolders(slave string) []string {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return []string{fmt.Sprintf("%s cannot be read, so no holder of %s is named: %v", procRoot, slave, err)}
	}
	var holders []string
	unreadable := map[string]int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fds, err := openDescriptors(pid)
		switch {
		case errors.Is(err, os.ErrPermission):
			unreadable[ownerOf(pid)]++
			continue
		case err != nil:
			continue
		}
		var onTerminal []string
		var others []string
		for _, fd := range fds {
			switch fd.target {
			case slave:
				onTerminal = append(onTerminal, fd.number)
			case controllingTerminal:
				onTerminal = append(onTerminal, fd.number+" (opened as "+controllingTerminal+", so perhaps another terminal)")
			default:
				others = append(others, fd.number+"->"+fd.target)
			}
		}
		if len(onTerminal) == 0 {
			continue
		}
		holders = append(holders, fmt.Sprintf("pid %d holds %s on descriptor %s: %s (its other descriptors: %s)",
			pid, slave, strings.Join(onTerminal, ", "), commandLine(pid), strings.Join(others, " ")))
	}
	if len(unreadable) != 0 {
		owners := make([]string, 0, len(unreadable))
		for owner, count := range unreadable {
			owners = append(owners, fmt.Sprintf("%d of user %s", count, owner))
		}
		slices.Sort(owners)
		holders = append(holders, "processes whose descriptors this user cannot read, not named: "+strings.Join(owners, ", "))
	}
	return holders
}

// descriptor is one open file of a process: its number, and what it names.
type descriptor struct {
	number, target string
}

// openDescriptors lists what a process holds open, by number. A process that ended while it was read has none, and
// reads as an error that is no permission error.
func openDescriptors(pid int) ([]descriptor, error) {
	dir := fmt.Sprintf("%s/%d/fd", procRoot, pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []descriptor
	for _, entry := range entries {
		target, err := os.Readlink(dir + "/" + entry.Name())
		if err != nil {
			continue
		}
		out = append(out, descriptor{number: entry.Name(), target: target})
	}
	slices.SortFunc(out, func(a, b descriptor) int {
		an, _ := strconv.Atoi(a.number)
		bn, _ := strconv.Atoi(b.number)
		return an - bn
	})
	return out, nil
}

// commandLineBytes bounds a command line a holder is named by, because a shell started with a script as its argument
// has the whole script for its command line.
const commandLineBytes = 160

// commandLine is what a process was started with, as its arguments on one line and cut at commandLineBytes, or what
// stands in for it when it ended or hides it.
func commandLine(pid int) string {
	raw, err := os.ReadFile(fmt.Sprintf("%s/%d/cmdline", procRoot, pid))
	if err != nil {
		return "(command line unreadable: " + err.Error() + ")"
	}
	if line := strings.Join(strings.Fields(strings.ReplaceAll(string(raw), "\x00", " ")), " "); line != "" {
		if len(line) > commandLineBytes {
			line = strings.ToValidUTF8(line[:commandLineBytes], "") + "..."
		}
		return line
	}
	comm, err := os.ReadFile(fmt.Sprintf("%s/%d/comm", procRoot, pid))
	if err != nil {
		return "(no command line)"
	}
	return "[" + strings.TrimSpace(string(comm)) + "]"
}

// ownerOf is the user ID a process runs as, which any user may read.
func ownerOf(pid int) string {
	raw, err := os.ReadFile(fmt.Sprintf("%s/%d/status", procRoot, pid))
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if fields := strings.Fields(strings.TrimPrefix(line, "Uid:")); strings.HasPrefix(line, "Uid:") && len(fields) > 0 {
			return fields[0]
		}
	}
	return "unknown"
}
