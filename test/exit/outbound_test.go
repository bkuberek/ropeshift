package exit

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// traceReport is what one `strace -f` log shows about the network.
type traceReport struct {
	// programs are the programs processes ran, each as its file's name and, for ropeshift, the
	// command it ran, such as "ropeshift hook".
	programs map[string]bool
	// unix and loopback count connections to a local socket and to a loopback address.
	unix, loopback int
	// offLoopback lists each call that reached for an address off loopback, or for an address this
	// reader cannot judge, with the program that made it.
	offLoopback []string
}

var (
	traceLine    = regexp.MustCompile(`^(\d+)\s+(\w+)\((.*)$`)
	traceResumed = regexp.MustCompile(`^(\d+)\s+<\.\.\. (\w+) resumed>.*= (\d+)$`)
	traceFamily  = regexp.MustCompile(`sa_family=(AF_\w+)`)
	traceInet    = regexp.MustCompile(`sin_addr=inet_addr\("([^"]*)"\)`)
	traceInet6   = regexp.MustCompile(`inet_pton\(AF_INET6, "([^"]*)"`)
	traceArgv    = regexp.MustCompile(`^"([^"]*)", \["([^"]*)"(?:, "([^"]*)")?`)
	traceReturn  = regexp.MustCompile(`= (\d+)$`)
)

// readTrace reads a log of `strace -f` that records the calls which start a process, a thread, or a
// program, and the calls that carry an address. A call carries a thread's ID, so each new thread or
// process takes its parent's program until it runs one of its own. A vfork child can run its program
// before its parent's clone returns, so a clone's return never takes back a program the child runs.
// Process IDs do not come round within one run, and if one did, only the name a call is reported
// under could be wrong, never whether it is reported.
func readTrace(r io.Reader) (traceReport, error) {
	report := traceReport{programs: map[string]bool{}}
	owner := map[string]string{}
	ran := map[string]bool{}
	programOf := func(id string) string {
		if name, ok := owner[id]; ok {
			return name
		}
		return "process " + id
	}
	inherit := func(child, parent string) {
		if !ran[child] {
			owner[child] = programOf(parent)
		}
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if resumed := traceResumed.FindStringSubmatch(line); resumed != nil {
			if isClone(resumed[2]) {
				inherit(resumed[3], resumed[1])
			}
			continue
		}
		call := traceLine.FindStringSubmatch(line)
		if call == nil {
			continue
		}
		id, name, rest := call[1], call[2], call[3]
		switch {
		case name == "execve":
			argv := traceArgv.FindStringSubmatch(rest)
			if argv == nil || strings.Contains(rest, "= -1 ") {
				continue
			}
			program := programName(argv[2], argv[3])
			owner[id], ran[id] = program, true
			report.programs[program] = true
		case isClone(name):
			if ret := traceReturn.FindStringSubmatch(rest); ret != nil {
				inherit(ret[1], id)
			}
		default:
			report.judge(name, rest, programOf(id))
		}
	}
	return report, scanner.Err()
}

func isClone(name string) bool {
	return slices.Contains([]string{"clone", "clone3", "fork", "vfork"}, name)
}

// programName names a program by the name it started under, and ropeshift by the command it ran as
// well, because one binary is the daemon, the CLI, the hook, the MCP server, and the shim, and the
// name picks which.
func programName(started, command string) string {
	name := filepath.Base(started)
	if (name == "ropeshift" || name == "ropeshiftd") && command != "" && !strings.HasPrefix(command, "-") {
		return name + " " + command
	}
	return name
}

// judge reads the addresses one call names. A local socket, the kernel's netlink, and an empty
// address leave the machine for nothing, a loopback address stays on it, and anything else fails.
func (report *traceReport) judge(call, args, program string) {
	for _, family := range traceFamily.FindAllStringSubmatch(args, -1) {
		switch family[1] {
		case "AF_UNIX":
			if call == "connect" {
				report.unix++
			}
		case "AF_NETLINK", "AF_UNSPEC":
		case "AF_INET", "AF_INET6":
			addresses := traceInet.FindAllStringSubmatch(args, -1)
			if family[1] == "AF_INET6" {
				addresses = traceInet6.FindAllStringSubmatch(args, -1)
			}
			if len(addresses) == 0 {
				report.fail(program, call, "an address this reader cannot read", args)
				continue
			}
			for _, address := range addresses {
				ip := net.ParseIP(address[1])
				if ip == nil || !ip.IsLoopback() {
					report.fail(program, call, strconv.Quote(address[1]), args)
					continue
				}
				if call == "connect" {
					report.loopback++
				}
			}
		default:
			report.fail(program, call, family[1], args)
		}
	}
}

func (report *traceReport) fail(program, call, what, args string) {
	report.offLoopback = append(report.offLoopback, fmt.Sprintf("%s: %s to %s: %s(%s", program, call, what, call, args))
}

// TestTheTraceReaderFindsEveryAddressOffLoopback holds the reader TestNoOutboundConnections judges by,
// on lines in the form strace 6 prints them, so a check that passes cannot pass because it read nothing.
func TestTheTraceReaderFindsEveryAddressOffLoopback(t *testing.T) {
	const prelude = `100 execve("/opt/ropeshift/bin/ropeshiftd", ["ropeshiftd", "solo", "--source", "/home/u/project"], 0xc0000a0000 /* 9 vars */) = 0
100 clone(child_stack=0xc000080000, flags=CLONE_VM|CLONE_FS|CLONE_FILES|CLONE_SIGHAND|CLONE_THREAD|CLONE_SYSVSEM|CLONE_SETTLS, tls=0xc000060090) = 101
100 clone3({flags=CLONE_VM|CLONE_VFORK|CLONE_CLEAR_SIGHAND, exit_signal=SIGCHLD, stack=0xc000100000, stack_size=0x8000} <unfinished ...>
100 <... clone3 resumed> => {parent_tid=[0]}, 88) = 102
102 execve("/opt/agent/claude", ["/opt/agent/claude", "-p", "--output-format", "stream-json"], 0xc0001b4000 /* 30 vars */) = 0
`
	tests := []struct {
		name     string
		line     string
		unix     int
		loopback int
		offBy    string
	}{
		{"a local socket", `101 connect(7, {sa_family=AF_UNIX, sun_path="/home/u/.ropeshift/run/control.sock"}, 41) = 0`, 1, 0, ""},
		{"the receiver on loopback", `102 connect(3, {sa_family=AF_INET, sin_port=htons(7318), sin_addr=inet_addr("127.0.0.1")}, 16) = -1 EINPROGRESS (Operation now in progress)`, 0, 1, ""},
		{"loopback over IPv6", `102 connect(3, {sa_family=AF_INET6, sin6_port=htons(7317), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "::1", &sin6_addr), sin6_scope_id=0}, 28) = 0`, 0, 1, ""},
		{"loopback mapped into IPv6", `102 connect(3, {sa_family=AF_INET6, sin6_port=htons(7317), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "::ffff:127.0.0.1", &sin6_addr), sin6_scope_id=0}, 28) = 0`, 0, 1, ""},
		{"a listener on loopback", `101 bind(8, {sa_family=AF_INET, sin_port=htons(7317), sin_addr=inet_addr("127.0.0.1")}, 16) = 0`, 0, 0, ""},
		{"the kernel's netlink", `101 sendto(9, [{nlmsg_len=20, nlmsg_type=RTM_GETLINK}], 20, 0, {sa_family=AF_NETLINK, nl_pid=0, nl_groups=00000000}, 12) = 20`, 0, 0, ""},
		{"an address taken back", `101 connect(4, {sa_family=AF_UNSPEC, sa_data="\0\0\0\0\0\0\0\0\0\0\0\0\0\0"}, 16) = 0`, 0, 0, ""},
		{"a connected socket's send", `101 sendto(4, "\1\2", 2, MSG_NOSIGNAL, NULL, 0) = 2`, 0, 0, ""},
		{"the daemon dials out", `101 connect(5, {sa_family=AF_INET, sin_port=htons(443), sin_addr=inet_addr("192.0.2.1")}, 16) = -1 ENETUNREACH (Network is unreachable)`, 0, 0, "ropeshiftd solo"},
		{"the session dials out over IPv6", `102 connect(5, {sa_family=AF_INET6, sin6_port=htons(443), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "2001:db8::1", &sin6_addr), sin6_scope_id=0}, 28 <unfinished ...>`, 0, 0, "claude"},
		{"a name lookup", `101 sendmsg(6, {msg_name={sa_family=AF_INET, sin_port=htons(53), sin_addr=inet_addr("192.0.2.53")}, msg_namelen=16, msg_iov=[{iov_base="\x12\x34", iov_len=2}], msg_iovlen=1, msg_controllen=0, msg_flags=0}, 0) = 2`, 0, 0, "ropeshiftd solo"},
		{"a listener on every address", `101 bind(8, {sa_family=AF_INET, sin_port=htons(7317), sin_addr=inet_addr("0.0.0.0")}, 16) = 0`, 0, 0, "ropeshiftd solo"},
		{"a raw socket", `101 sendto(8, "\0", 1, 0, {sa_family=AF_PACKET, sll_protocol=htons(ETH_P_ALL), sll_ifindex=1}, 20) = 1`, 0, 0, "ropeshiftd solo"},
		{"an address the reader cannot read", `101 connect(5, {sa_family=AF_INET, sin_port=htons(80), ...}, 16) = 0`, 0, 0, "ropeshiftd solo"},
		{"a process that never ran a program", `999 connect(5, {sa_family=AF_INET, sin_port=htons(80), sin_addr=inet_addr("192.0.2.1")}, 16) = 0`, 0, 0, "process 999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := readTrace(strings.NewReader(prelude + tt.line + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			if !report.programs["ropeshiftd solo"] || !report.programs["claude"] {
				t.Errorf("the programs read: %v", report.programs)
			}
			if report.unix != tt.unix || report.loopback != tt.loopback {
				t.Errorf("local sockets %d and loopback %d, want %d and %d", report.unix, report.loopback, tt.unix, tt.loopback)
			}
			switch {
			case tt.offBy == "" && len(report.offLoopback) != 0:
				t.Errorf("a call that stays on the machine failed: %v", report.offLoopback)
			case tt.offBy != "" && (len(report.offLoopback) != 1 || !strings.HasPrefix(report.offLoopback[0], tt.offBy+": ")):
				t.Errorf("want one call off loopback by %s, got %v", tt.offBy, report.offLoopback)
			}
		})
	}
}

// A failed execve, as a shell's search of PATH makes, runs nothing, so the process keeps its program.
func TestTheTraceReaderSkipsAProgramThatDidNotStart(t *testing.T) {
	log := `100 execve("/opt/ropeshift/bin/ropeshift", ["ropeshift", "hook", "PostToolUse"], 0xc0000a0000 /* 9 vars */) = 0
100 execve("/usr/local/bin/git", ["git", "status"], 0xc0000a0000 /* 9 vars */) = -1 ENOENT (No such file or directory)
100 connect(5, {sa_family=AF_INET, sin_port=htons(80), sin_addr=inet_addr("192.0.2.1")}, 16) = 0
`
	report, err := readTrace(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if report.programs["git"] || !report.programs["ropeshift hook"] {
		t.Errorf("the programs read: %v", report.programs)
	}
	if len(report.offLoopback) != 1 || !strings.HasPrefix(report.offLoopback[0], "ropeshift hook: ") {
		t.Errorf("the call off loopback: %v", report.offLoopback)
	}
}

// A vfork child can run its program before strace prints its parent's return from the clone, so a
// clone's return never takes back the program the child already runs.
func TestTheTraceReaderKeepsAProgramAChildRanBeforeItsCloneReturned(t *testing.T) {
	log := `40 execve("/opt/ropeshift/bin/ropeshift", ["ropeshift", "init"], 0x1ce8d493a0c0 /* 6 vars */ <unfinished ...>
40 <... execve resumed>)             = 0
40 clone(child_stack=NULL, flags=CLONE_VM|CLONE_PIDFD|CLONE_VFORK|SIGCHLD <unfinished ...>
48 execve("/opt/ropeshift/bin/ropeshift", ["ropeshiftd", "solo", "--web", "off"], 0x4ff46d5facc0 /* 6 vars */ <unfinished ...>
40 <... clone resumed>, parent_tid=[12]) = 48
48 <... execve resumed>)             = 0
48 clone(child_stack=0x228db60fa000, flags=CLONE_VM|CLONE_FS|CLONE_FILES|CLONE_SIGHAND|CLONE_THREAD|CLONE_SYSVSEM) = 49
49 connect(7, {sa_family=AF_INET, sin_port=htons(443), sin_addr=inet_addr("192.0.2.1")}, 16) = -1 ENETUNREACH (Network is unreachable)
48 connect(7, {sa_family=AF_INET, sin_port=htons(443), sin_addr=inet_addr("192.0.2.1")}, 16) = -1 ENETUNREACH (Network is unreachable)
`
	report, err := readTrace(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.offLoopback) != 2 {
		t.Fatalf("the calls off loopback: %v", report.offLoopback)
	}
	for _, call := range report.offLoopback {
		if !strings.HasPrefix(call, "ropeshiftd solo: ") {
			t.Errorf("a call by the daemon is named %s", call)
		}
	}
}

// One binary is every command, chosen by the name it starts under, as ropeshift init starts the daemon
// from its own file under the daemon's name.
func TestTheTraceReaderNamesAProgramByTheNameItStartedUnder(t *testing.T) {
	log := `100 execve("/opt/ropeshift/bin/ropeshift", ["ropeshiftd", "solo", "--web", "off"], 0xc0000a0000 /* 9 vars */) = 0
101 execve("/opt/agent/claude", ["/opt/agent/claude", "--version"], 0xc0000a0000 /* 9 vars */) = 0
`
	report, err := readTrace(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.programs) != 2 || !report.programs["ropeshiftd solo"] || !report.programs["claude"] {
		t.Errorf("the programs read: %v", report.programs)
	}
}
