#!/usr/bin/env bash
# Exercise scripts/deploy.sh against a transient systemd user unit, never the live proxy.
# Usage: scripts/deploy-test.sh   (CPA_TEST_PORT picks a port in 18390-18399)
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
deploy="$repo/scripts/deploy.sh"
unit="${CPA_TEST_UNIT:-cpa-deploy-test.service}"
port="${CPA_TEST_PORT:-18390}"
fails=0

die() { echo "deploy-test: $*" >&2; exit 3; }

# The live proxy carries real traffic, so refuse its name and its port.
[[ "$unit" != "cli-proxy-api.service" && "$unit" != "cli-proxy-api" ]] || die "refusing to touch the live unit"
[[ "$port" =~ ^1839[0-9]$ ]] || die "port must be in 18390-18399, got $port"
[[ "$unit" == cpa-deploy-test* ]] || die "unit name must start with cpa-deploy-test"
[[ -z "$(ss -ltn "sport = :$port" | tail -n +2)" ]] || die "port $port is already in use"

dir="$(mktemp -d /tmp/cpa-deploy-test.XXXXXX)"
tmp="$(mktemp -d /tmp/cpa-deploy-test-wt.XXXXXX)"
usctl() { systemctl --user "$@"; }

cleanup() {
  exec 8>&- 2>/dev/null || true
  usctl stop "$unit" >/dev/null 2>&1 || true
  usctl reset-failed "$unit" >/dev/null 2>&1 || true
  git -C "$repo" worktree remove --force "$tmp/broken" >/dev/null 2>&1 || true
  git -C "$repo" worktree prune
  chmod -R u+rwX "$dir" 2>/dev/null || true
  rm -rf "$dir" "$tmp"
}
trap cleanup EXIT

main_pid() { usctl show -p MainPID --value "$unit"; }
exe_of_main() { readlink "/proc/$(main_pid)/exe" 2>/dev/null || true; }
healthz() { curl -fsS -m 2 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; }

wait_healthz() {
  for _ in $(seq "${1:-30}"); do
    healthz && return 0
    sleep 1
  done
  return 1
}

# expect <description> <command...>: record a failed assertion for the current case.
expect() {
  local what="$1"
  shift
  if ! "$@"; then
    echo "    assertion failed: $what"
    case_ok=0
  fi
}

# run_deploy <log name> <args...>: sets rc and out; the log is kept in the temp dir.
run_deploy() {
  local name="$1"
  shift
  rc=0
  out="$("$deploy" --unit "$unit" --dir "$dir" --port "$port" "$@" 2>&1)" || rc=$?
  printf '%s\n' "$out" >"$dir/$name.log"
}

begin() { echo "case $1: $2"; case_ok=1; }
finish() {
  if ((case_ok)); then
    echo "PASS case $1"
  else
    echo "FAIL case $1"
    sed 's/^/    | /' "$dir/$2.log" 2>/dev/null || true
    fails=$((fails + 1))
  fi
}

eq() { [[ "$1" == "$2" ]]; }
same_file() { cmp -s "$1" "$2"; }
differs() { ! cmp -s "$1" "$2"; }
new_pid() { [[ "$1" != 0 && "$1" != "$2" ]]; }
nonzero() { [[ "$1" != 0 ]]; }
at_most() { (($1 <= $2)); }
has() { grep -qi -- "$1" <<<"$2"; }
lacks() { ! has "$1" "$2"; }

seed_install() {
  mkdir -p "$dir/static" "$dir/auths"
  sed -e "s/^  port: .*/  port: $port/" \
    -e 's/^  host: .*/  host: "127.0.0.1"/' \
    -e 's/^  # disable-auto-update-panel: false/  disable-auto-update-panel: true/' \
    -e "s|^  auth-dir: .*|  auth-dir: \"$dir/auths\"|" \
    "$repo/config.example.yaml" >"$dir/config.yaml"
  (cd "$repo" && go build -o "$dir/cli-proxy-api" ./cmd/server)
  echo placeholder >"$dir/static/management.html"
  cp "$dir/static/management.html" "$dir/placeholder.html"
}

start_unit() {
  usctl stop "$unit" >/dev/null 2>&1 || true
  usctl reset-failed "$unit" >/dev/null 2>&1 || true
  systemd-run --user --quiet --unit="${unit%.service}" \
    -p WorkingDirectory="$dir" -p Restart=on-failure -p StartLimitIntervalSec=0 \
    "$dir/cli-proxy-api" --config config.yaml --no-browser --local-model
  wait_healthz 30 || die "test unit never became healthy"
}

head_sha="$(git -C "$repo" rev-parse HEAD)"

echo "setup: install dir $dir, unit $unit, port $port"
seed_install
start_unit
pid0="$(main_pid)"

begin 1 "deploy HEAD ($head_sha)"
run_deploy case1 HEAD
expect "deploy.sh exits 0 (got $rc)" eq "$rc" 0
expect "DEPLOYED holds HEAD sha" eq "$(cut -d' ' -f1 "$dir/DEPLOYED" 2>/dev/null)" "$head_sha"
expect "panel differs from placeholder" differs "$dir/static/management.html" "$dir/placeholder.html"
expect "panel equals the release panel" same_file "$dir/static/management.html" "$dir/releases/$head_sha/management.html"
expect "binary equals the release binary" same_file "$dir/cli-proxy-api" "$dir/releases/$head_sha/cli-proxy-api"
expect "MainPID changed" new_pid "$(main_pid)" "$pid0"
expect "running exe is the install path" eq "$(exe_of_main)" "$dir/cli-proxy-api"
expect "healthz answers" healthz
finish 1 case1
pid1="$(main_pid)"

begin 2 "redeploy the same sha reuses the release"
run_deploy case2 HEAD
expect "deploy.sh exits 0 (got $rc)" eq "$rc" 0
expect "output says the release was reused" has "reusing release" "$out"
expect "no build ran" lacks "built " "$out"
expect "MainPID changed" new_pid "$(main_pid)" "$pid1"
expect "healthz answers" healthz
finish 2 case2
cp "$dir/static/management.html" "$dir/live-panel.html" || true
cp "$dir/DEPLOYED" "$dir/live-deployed" || true

begin 3 "broken release rolls back"
git -C "$repo" worktree add --detach -q "$tmp/broken" HEAD
sed -i '/^func main() {$/a\	os.Exit(3)' "$tmp/broken/cmd/server/main.go"
git -C "$tmp/broken" -c user.name=deploy-test -c user.email=deploy-test@example.invalid \
  -c core.hooksPath=/dev/null commit -q -a -m "deploy-test: broken main"
bad_sha="$(git -C "$tmp/broken" rev-parse HEAD)"
run_deploy case3 "$bad_sha"
expect "deploy.sh exits 1 (got $rc)" eq "$rc" 1
expect "unit is healthy again" wait_healthz 30
expect "running exe is the install path" eq "$(exe_of_main)" "$dir/cli-proxy-api"
expect "binary is the case 1 binary" same_file "$dir/cli-proxy-api" "$dir/releases/$head_sha/cli-proxy-api"
expect "panel unchanged" same_file "$dir/static/management.html" "$dir/live-panel.html"
expect "DEPLOYED unchanged" same_file "$dir/DEPLOYED" "$dir/live-deployed"
finish 3 case3

begin 4 "a held lock stops a second run"
exec 8>"$dir/.deploy.lock"
flock -n 8
SECONDS=0
run_deploy case4 HEAD
elapsed=$SECONDS
exec 8>&-
expect "deploy.sh exits non-zero" nonzero "$rc"
expect "output names the lock" has lock "$out"
expect "it returned within 5s (took ${elapsed}s)" at_most "$elapsed" 5
finish 4 case4

begin 5 "an unreadable release binary rolls back"
chmod 000 "$dir/releases/$bad_sha/cli-proxy-api"
run_deploy case5 "$bad_sha"
chmod 644 "$dir/releases/$bad_sha/cli-proxy-api"
expect "deploy.sh exits 1 (got $rc)" eq "$rc" 1
expect "unit is healthy" wait_healthz 30
expect "running exe is the install path" eq "$(exe_of_main)" "$dir/cli-proxy-api"
expect "binary is the case 1 binary" same_file "$dir/cli-proxy-api" "$dir/releases/$head_sha/cli-proxy-api"
expect "panel unchanged" same_file "$dir/static/management.html" "$dir/live-panel.html"
expect "DEPLOYED unchanged" same_file "$dir/DEPLOYED" "$dir/live-deployed"
finish 5 case5

begin 6 "a config that leaves the panel auto-update on stops the deploy"
pid6="$(main_pid)"
cp -p "$dir/config.yaml" "$dir/config.yaml.good"
sed -i 's/^\([[:space:]]*\)disable-auto-update-panel:.*/\1# disable-auto-update-panel: true/' "$dir/config.yaml"
run_deploy case6 HEAD
mv "$dir/config.yaml.good" "$dir/config.yaml"
expect "deploy.sh exits 3 (got $rc)" eq "$rc" 3
expect "output names the panel auto-update" has "disable-auto-update-panel" "$out"
expect "MainPID unchanged" eq "$(main_pid)" "$pid6"
expect "healthz answers" healthz
expect "binary is the case 1 binary" same_file "$dir/cli-proxy-api" "$dir/releases/$head_sha/cli-proxy-api"
expect "panel unchanged" same_file "$dir/static/management.html" "$dir/live-panel.html"
expect "DEPLOYED unchanged" same_file "$dir/DEPLOYED" "$dir/live-deployed"
finish 6 case6

if ((fails)); then
  echo "FAILED: $fails case(s)"
  exit 1
fi
echo "all cases passed"
