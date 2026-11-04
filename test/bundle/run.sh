#!/bin/sh
# Runs the Linux check from the bundle `make linux-bundle` writes, on a machine with no Go: the pty
# tests, the scope check, the shim and pane kill tests, the service tests under the user's systemd
# manager, and the bench. It needs a shell, tmux, git, and a systemd user manager.
#
# It never runs claude: each test puts fakeclaude or the canary first on PATH, and this script puts the
# canary first on its own. Every daemon a test starts has the web listener and telemetry off, or
# telemetry on a port the kernel picks, so the default ports 7317 and 7318 stay as they were. It writes
# only in this directory and in the unit files of the service names this run owns. On exit, after a
# failure too, it removes those units and stops each process that came from this directory, and it ends
# with a listing of what remains of the run, which is nothing.
#
#   sh run.sh                  every step, in order
#   sh run.sh service bench    only the steps named
#
# results.txt holds the summary, and logs/ the whole output of each step.
set -u

here=$(cd "$(dirname "$0")" && pwd -P)
results="$here/results.txt"
logs="$here/logs"
scratch="$here/t"
prefix="ropeshift-test-bundle-$(date +%Y%m%d%H%M%S)-$$-"
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
all="pty scope kills service bench"
failed=0
ports_before=
running=
cd / || exit 2

say() {
	printf '%s\n' "$*" | tee -a "$results"
}

refuse() {
	say "refused: $*"
	exit 2
}

# keep is this script and the shells above it, which a listing of the bundle's processes leaves out.
keep=" "
pid=$$
while [ "$pid" -gt 0 ]; do
	keep="$keep$pid "
	parent=0
	while read -r key value; do
		if [ "$key" = "PPid:" ]; then
			parent=$value
		fi
	done <"/proc/$pid/status"
	pid=$parent
done

# ours lists each process that came from this bundle: its program, its working directory, or its
# command line is in this directory. Every test runs with its working directory here, so a process a
# test starts with a program from elsewhere still counts.
ours() {
	for dir in /proc/[0-9]*; do
		pid=${dir#/proc/}
		case "$keep" in *" $pid "*) continue ;; esac
		exe=$(readlink "$dir/exe" 2>/dev/null) || exe=
		cwd=$(readlink "$dir/cwd" 2>/dev/null) || cwd=
		cmd=$(tr '\0' '|' <"$dir/cmdline" 2>/dev/null) || cmd=
		fields="|$exe|$cwd|$cmd|"
		case "$fields" in *"|$here/run.sh|"*) continue ;; esac
		case "$fields" in
		*"|$here|"* | *"|$here/"* | *"=$here/"* | *":$here/"*) echo "$pid" ;;
		esac
	done
}

# stop_ours ends what this run left, by the process IDs ours finds, and checks each again before the
# last signal, so an ID the system gave to another process is left alone.
stop_ours() {
	pids=$(ours)
	if [ -z "$pids" ]; then
		return 0
	fi
	say "stopping what this run left: $(echo "$pids" | tr '\n' ' ')"
	# shellcheck disable=SC2086 # one word for each process
	kill -TERM $pids 2>/dev/null
	tries=0
	while [ "$tries" -lt 25 ]; do
		pids=$(ours)
		if [ -z "$pids" ]; then
			return 0
		fi
		sleep 0.2
		tries=$((tries + 1))
	done
	# shellcheck disable=SC2086 # one word for each process
	kill -KILL $pids 2>/dev/null
}

# remove_units stops, disables, and removes every service of this run's names, and what enabling one
# linked, and has the manager forget them.
remove_units() {
	systemctl --user list-units --all --plain --no-legend "$prefix*" 2>/dev/null | while read -r unit _; do
		systemctl --user stop "$unit" >/dev/null 2>&1
	done
	for file in "$unit_dir/$prefix"*.service; do
		if [ -e "$file" ]; then
			systemctl --user disable --now "${file##*/}" >/dev/null 2>&1
			rm -f "$file"
		fi
	done
	for link in "$unit_dir"/*.wants/"$prefix"*.service; do
		if [ -L "$link" ] || [ -e "$link" ]; then
			rm -f "$link"
		fi
	done
	systemctl --user daemon-reload >/dev/null 2>&1
	systemctl --user reset-failed "$prefix*" >/dev/null 2>&1
}

# listening names which of the default ports have a listener.
listening() {
	for table in /proc/net/tcp /proc/net/tcp6; do
		if [ -r "$table" ]; then
			while read -r _ local _ state _; do
				case "$state:${local##*:}" in
				0A:1C95) echo 7317 ;;
				0A:1C96) echo 7318 ;;
				esac
			done <"$table"
		fi
	done | sort -u | tr '\n' ' '
}

listing() {
	say "== what remains of this run"
	files=
	for file in "$unit_dir/$prefix"*.service "$unit_dir"/*.wants/"$prefix"*.service; do
		if [ -L "$file" ] || [ -e "$file" ]; then
			files="$files $file"
		fi
	done
	units=$(systemctl --user list-units --all --plain --no-legend "$prefix*" 2>/dev/null | tr '\n' ' ')
	pids=$(ours | tr '\n' ' ')
	scratch_left=removed
	if [ -e "$scratch" ]; then
		scratch_left="$scratch is still there"
	fi
	ports=$(listening)
	say "unit files named $prefix*: ${files:-none}"
	say "units the manager holds under that name: ${units:-none}"
	say "processes from this bundle: ${pids:-none}"
	say "scratch directory: $scratch_left"
	say "listeners on 7317 and 7318: before ${ports_before:-none}, after ${ports:-none}"
	if [ -n "$files$units$pids" ] || [ -e "$scratch" ] || [ "$ports" != "$ports_before" ]; then
		say "something of this run remains"
		failed=1
	else
		say "nothing of this run remains"
	fi
}

cleanup() {
	trap - EXIT INT TERM HUP
	if [ -n "$running" ]; then
		kill -TERM "$running" 2>/dev/null
		wait "$running" 2>/dev/null
	fi
	remove_units
	stop_ours
	remove_units
	rm -rf "$scratch"
	if [ -e "$here/bin/canary/tripped" ]; then
		say "the claude canary ran"
		rm -f "$here/bin/canary/tripped"
		failed=1
	fi
	listing
}

# step runs one compiled test binary with its working directory in the scratch directory, and keeps
# its result lines.
step() {
	name=$1
	binary=$2
	shift 2
	say "== $name: tests/$binary $*"
	started=$(date +%s)
	# In the background, so a signal to this script reaches its trap while a step runs.
	(cd "$scratch" && exec "$here/tests/$binary" -test.v -test.count=1 "$@") >"$logs/$name.log" 2>&1 &
	running=$!
	wait "$running"
	status=$?
	running=
	while IFS= read -r line; do
		trimmed=${line#"${line%%[! ]*}"}
		case "$trimmed" in
		"--- "* | ok* | PASS | FAIL* | panic:* | *_test.go:[0-9]*) say "$line" ;;
		esac
	done <"$logs/$name.log"
	say "$name: exit $status after $(($(date +%s) - started))s"
	if [ "$status" -ne 0 ]; then
		failed=1
	fi
	stop_ours
	remove_units
}

: >"$results"
ports_before=$(listening)
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

[ "$(id -u)" != 0 ] || refuse "run it as the user whose systemd manager it tests, not as root"
for tool in tmux git systemctl systemd-run ps readlink tr tee sort; do
	command -v "$tool" >/dev/null 2>&1 || refuse "no $tool on PATH"
done
if [ -z "${XDG_RUNTIME_DIR:-}" ] || [ ! -e "$XDG_RUNTIME_DIR/systemd/private" ]; then
	refuse "no systemd user manager answers in XDG_RUNTIME_DIR"
fi
if [ "${#scratch}" -gt 20 ]; then
	refuse "$scratch is longer than 20 characters, which leaves no room for a socket below it: put the bundle at a shorter path"
fi
# Through tr, because a sysctl file answers only the first read, and the shell's read takes one byte.
range=$(tr -s '\t' ' ' </proc/sys/net/ipv4/ip_local_port_range)
low=${range%% *}
high=${range##* }
case "$low:$high" in
[0-9]*:[0-9]*) ;;
*) refuse "cannot read the ephemeral port range: $range" ;;
esac
for port in 7317 7318; do
	if [ "$port" -ge "$low" ] && [ "$port" -le "$high" ]; then
		refuse "port $port is in the range $low-$high that a test's port 0 draws from"
	fi
done
for name in "$@"; do
	case " $all " in *" $name "*) ;; *) refuse "no step $name: the steps are $all" ;; esac
done

mkdir -p "$scratch" "$logs"
export ROPESHIFT_TEST_PREBUILT="$here/bin"
export ROPESHIFT_TEST_SERVICE_PREFIX="$prefix"
export TMPDIR="$scratch"
export PATH="$here/bin/canary:$PATH"
export ROPESHIFT_WEB=off ROPESHIFT_TELEMETRY=off

os=unknown
if [ -r /etc/os-release ]; then
	os=$(. /etc/os-release && echo "$PRETTY_NAME")
fi
say "== ropeshift Linux check, $(date -u +%Y-%m-%dT%H:%M:%SZ)"
say "system: $os, $(uname -srm)"
say "tools: $(tmux -V), $(git --version), $(systemctl --version | head -n 1)"
say "go on PATH: $(command -v go || echo none)"
say "ephemeral ports: $low-$high, listeners on 7317 and 7318: ${ports_before:-none}"
say "service names: $prefix*"

if [ "$#" -eq 0 ]; then
	# shellcheck disable=SC2086 # one word for each step
	set -- $all
fi
for name in "$@"; do
	case $name in
	pty) step pty localauth.test -test.timeout 5m ;;
	scope) step scope contract.test -test.timeout 5m -test.run '^TestAnOutlivingProcessLeavesTheControlGroup$' ;;
	kills) step kills chaos.test -test.timeout 30m -test.run '^(TestShimWorkerKilledMidStream|TestShimWorkerGroupKilled|TestShimNoWorkerAttached|TestPlainPipeBlocks|TestCrashPaneStart|TestPaneRandomKills|TestCrashRestartChargesOnce)$' ;;
	service) step service service.test -test.timeout 15m ;;
	bench) step bench bench.test -test.timeout 20m ;;
	esac
done

cleanup
if [ "$failed" -ne 0 ]; then
	say "the check failed"
else
	say "the check passed"
fi
exit "$failed"
