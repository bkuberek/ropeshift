#!/bin/sh
# Runs the service tests on Linux under a real systemd user manager, in a container whose init is
# systemd, for a machine whose own manager the tests should not use, such as a CI runner. It writes the
# bundle `make linux-bundle` builds for the container engine's architecture, copies it into the
# container, and runs its scope and service steps there with test/bundle/run.sh, as a user whose
# manager runs through lingering.
#
# The container gets CAP_SYS_ADMIN and no other privilege: its entry point remounts its own cgroup2
# hierarchy writable and then starts systemd. AppArmor's default container profile forbids that mount,
# so the container runs unconfined by AppArmor.
#
# Usage: scripts/service-container.sh [OUTPUT]
#
#   OUTPUT  A directory that must not exist yet. It receives the bundle, results.txt, each step's log,
#           and the container's journal. By default, a new temporary directory.
#
# The container and its image are removed by their IDs when the script ends, after a failure too. A
# skipped test fails the run, because a skip means no user manager answered.

set -eu

fail() {
	printf 'service-container: %s\n' "$*" >&2
	exit 1
}

[ $# -le 1 ] || {
	printf 'usage: scripts/service-container.sh [OUTPUT]\n' >&2
	exit 2
}
out=${1:-}
if [ -z "$out" ]; then
	out=$(mktemp -d)/service
fi
[ ! -e "$out" ] || fail "$out exists"

cd "$(git rev-parse --show-toplevel)"

engine=$(docker version --format '{{.Server.Arch}}' 2>/dev/null) || fail "Docker is not available"
case $engine in
arm64 | aarch64) arch=arm64 ;;
amd64 | x86_64) arch=amd64 ;;
*) fail "no release targets the container engine's architecture $engine" ;;
esac

mkdir -p "$out"
make --no-print-directory linux-bundle BUNDLE="$out/bundle" BUNDLE_ARCH="$arch"

image=
container=
cleanup() {
	if [ -n "$container" ]; then
		docker rm --force "$container" >/dev/null || printf 'service-container: could not remove container %s\n' "$container" >&2
	fi
	if [ -n "$image" ]; then
		docker image rm "$image" >/dev/null || printf 'service-container: could not remove image %s\n' "$image" >&2
	fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

# systemd as init, the user session's D-Bus, and what test/bundle/run.sh needs.
printf '%s\n' \
	'FROM debian:bookworm-slim' \
	'ENV container=docker' \
	'RUN apt-get update && apt-get install --yes --no-install-recommends systemd systemd-sysv dbus dbus-user-session libpam-systemd tmux git procps && rm -rf /var/lib/apt/lists/* && useradd --create-home --shell /bin/sh tester' \
	'STOPSIGNAL SIGRTMIN+3' |
	docker build --quiet - >"$out/image-id"
image=$(cat "$out/image-id")
rm -f "$out/image-id"
[ -n "$image" ] || fail "the image build printed no ID"

container=$(docker create \
	--cap-add SYS_ADMIN \
	--security-opt apparmor=unconfined \
	--cgroupns private \
	--tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
	--entrypoint /bin/sh \
	"$image" -c 'mount -o remount,rw /sys/fs/cgroup && exec /sbin/init')
docker cp --quiet "$out/bundle/." "$container:/b"
docker start "$container" >/dev/null

# The system is up when systemd says so. Until systemd is the container's first process and its bus
# answers, systemctl reports offline or no bus. Degraded means a unit a container cannot run failed,
# which the tests do not use.
tries=0
while :; do
	[ "$(docker inspect --format '{{.State.Running}}' "$container")" = true ] ||
		fail "the container stopped: $(docker logs "$container" 2>&1 | tail -n 5)"
	state=$(docker exec "$container" systemctl is-system-running 2>&1) || true
	case $state in
	running | degraded) break ;;
	esac
	tries=$((tries + 1))
	[ "$tries" -lt 120 ] || fail "systemd did not start in the container: $state"
	sleep 1
done

docker exec "$container" chown -R tester:tester /b
docker exec "$container" loginctl enable-linger tester
uid=$(docker exec "$container" id -u tester)
tries=0
until docker exec "$container" test -S "/run/user/$uid/systemd/private"; do
	tries=$((tries + 1))
	[ "$tries" -lt 60 ] || fail "the user manager of tester never answered"
	sleep 1
done

status=0
docker exec --user tester --workdir /b \
	--env HOME=/home/tester \
	--env "XDG_RUNTIME_DIR=/run/user/$uid" \
	--env "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$uid/bus" \
	--env PATH=/usr/local/bin:/usr/bin:/bin \
	"$container" sh /b/run.sh scope service || status=$?

docker cp --quiet "$container:/b/results.txt" "$out/results.txt" || true
docker cp --quiet "$container:/b/logs" "$out/logs" || true
docker exec "$container" journalctl --no-pager --lines 400 >"$out/journal.txt" 2>&1 || true

[ "$status" -eq 0 ] || fail "test/bundle/run.sh exited $status; see $out/results.txt and $out/logs"
[ -f "$out/results.txt" ] || fail "the run wrote no results.txt"
if grep -q -- '--- SKIP' "$out/results.txt"; then
	fail "a test skipped, so the run proves nothing about the service manager; see $out/results.txt"
fi
for test in TestAnOutlivingProcessLeavesTheControlGroup TestServiceStopKeepsLanes; do
	grep -q -- "--- PASS: $test " "$out/results.txt" || fail "$test did not pass; see $out/results.txt"
done
printf 'service-container: the scope and service tests passed under systemd; results in %s\n' "$out"
