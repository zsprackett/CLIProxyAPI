#!/usr/bin/env bash
# Build the server binary and the management panel from ONE commit and install both.
# Usage: scripts/deploy.sh [--unit NAME] [--dir DIR] [--port N] [--build-only] [ref]
#   ref          commit to deploy (default origin/main)
#   --unit NAME  systemd user unit to restart (default cli-proxy-api.service)
#   --dir DIR    install dir (default: the unit's WorkingDirectory)
#   --port N     health port (default: first "port:" in DIR/config.yaml, else 8317)
#   --build-only build and stage the release, change nothing live
# Needs "disable-auto-update-panel: true" in DIR/config.yaml, else the server replaces the panel.
# Exit: 0 deployed, 1 unhealthy but rolled back, 2 rollback failed too, 3 nothing changed.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
unit=cli-proxy-api.service ref=origin/main dir="" port="" build_only=0

die() { echo "deploy: $*" >&2; exit 3; }
usage() { sed -n '2,10p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }
usctl() { systemctl --user "$@"; }
main_pid() { usctl show -p MainPID --value "$unit"; }

# Copy to a tmp name, then rename, so a reader never sees a half-written file.
put() {
  cp "$1" "$2.tmp-$$"
  [[ -z "${3:-}" ]] || chmod "$3" "$2.tmp-$$"
  mv "$2.tmp-$$" "$2"
}

while (($#)); do
  case "$1" in
    --unit) unit="${2:?--unit needs a value}"; shift 2 ;;
    --dir) dir="${2:?--dir needs a value}"; shift 2 ;;
    --port) port="${2:?--port needs a value}"; shift 2 ;;
    --build-only) build_only=1; shift ;;
    -h | --help) usage; exit 0 ;;
    -*) usage >&2; die "unknown option $1" ;;
    *) ref="$1"; shift ;;
  esac
done

[[ -n "$dir" ]] || dir="$(usctl show -p WorkingDirectory --value "$unit")"
[[ -n "$dir" ]] || die "unit $unit has no WorkingDirectory; pass --dir"
[[ -d "$dir" ]] || die "install dir $dir does not exist"
dir="$(cd "$dir" && pwd -P)"
if [[ -z "$port" ]]; then
  port="$(grep -m1 -E '^[[:space:]]*port:' "$dir/config.yaml" 2>/dev/null | sed -E 's/^[^:]*:[[:space:]]*([0-9]+).*/\1/' || true)"
  [[ "$port" =~ ^[0-9]+$ ]] || port=8317
fi

# One deploy at a time, for the whole run.
mkdir -p "$dir/releases"
exec 9>"$dir/.deploy.lock"
flock -n 9 || die "another deploy holds the lock $dir/.deploy.lock"

# A custom panel path means the panel is not under $dir/static.
if grep -qE '^[[:space:]]*(export[[:space:]]+)?(MANAGEMENT_STATIC_PATH|WRITABLE_PATH)=' "$dir/.env" 2>/dev/null; then
  die "$dir/.env sets MANAGEMENT_STATIC_PATH or WRITABLE_PATH; the panel path is elsewhere"
fi

# The server's panel auto-updater (at startup, then every 3 h) would replace the deployed panel
# with the GitHub release, so a deploy of a panel is only safe with it disabled.
if ((!build_only)) && ! grep -qE '^[[:space:]]*disable-auto-update-panel:[[:space:]]*true([[:space:]]|#|$)' "$dir/config.yaml" 2>/dev/null; then
  die "$dir/config.yaml does not set disable-auto-update-panel: true; the server's panel auto-updater would replace the deployed panel with the GitHub release"
fi

[[ "$ref" != origin/* ]] || git -C "$repo" fetch -q origin
sha="$(git -C "$repo" rev-parse --verify "$ref^{commit}")" || die "cannot resolve $ref"
release="$dir/releases/$sha"
stage="$release.tmp-$$"
work=""
trap '[[ -z "$work" ]] || git -C "$repo" worktree remove --force "$work/src" >/dev/null 2>&1; rm -rf "$work" "$stage"' EXIT

# Build both artifacts from a throwaway worktree of the exact commit.
build_release() {
  work="$(mktemp -d)" && mkdir -p "$stage" || return 1
  git -C "$repo" worktree add -q --detach "$work/src" "$sha" || return 1
  cd "$work/src" || return 1
  go build -ldflags "-X main.Version=$(git describe --tags --always) -X main.Commit=$sha -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o "$stage/cli-proxy-api" ./cmd/server || return 1
  PATH="$HOME/.bun/bin:$PATH" ./build-panel.sh "$stage/management.html" || return 1
  cd "$repo" && mv "$stage" "$release"
}

if [[ -f "$release/cli-proxy-api" && -f "$release/management.html" ]]; then
  echo "reusing release $sha"
  touch "$release"
else
  rm -rf "$release"
  build_release >&2 || die "build of $sha failed; nothing live changed"
fi

if ((build_only)); then
  echo "$release"
  exit 0
fi

# Healthy: unit active, a new MainPID running the installed file, /healthz answering.
healthy() {
  local old="$1" pid
  for _ in $(seq 30); do
    pid="$(main_pid)"
    if usctl is-active --quiet "$unit" && [[ "$pid" != 0 && "$pid" != "$old" ]] &&
      [[ "$(readlink "/proc/$pid/exe" 2>/dev/null)" == "$dir/cli-proxy-api" ]] &&
      curl -fsS -m 2 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

# Swap the binary by rename (cp over a running binary fails) and restart the unit.
# Callers run this as an if-condition, which disables set -e inside it: every step needs its own check.
install_binary() {
  local old
  old="$(main_pid)" || return 1
  put "$1" "$dir/cli-proxy-api" +x || return 1
  cmp -s "$1" "$dir/cli-proxy-api" || return 1
  usctl reset-failed "$unit" 2>/dev/null || true
  usctl restart "$unit" || true
  healthy "$old"
}

[[ -f "$dir/cli-proxy-api" ]] || die "no live binary at $dir/cli-proxy-api to roll back to"
mkdir -p "$dir/releases/rollback"
put "$dir/cli-proxy-api" "$dir/releases/rollback/cli-proxy-api" +x
rm -f "$dir/releases/rollback/management.html"
[[ ! -f "$dir/static/management.html" ]] || put "$dir/static/management.html" "$dir/releases/rollback/management.html"

echo "installing $sha to $unit"
if ! install_binary "$release/cli-proxy-api"; then
  journalctl --user -u "$unit" -n 40 --no-pager || true
  echo "deploy: $sha did not install or is unhealthy, rolling back the binary" >&2
  if install_binary "$dir/releases/rollback/cli-proxy-api"; then
    echo "deploy: rolled back; $unit is healthy on the previous binary" >&2
    exit 1
  fi
  echo "deploy: ROLLBACK FAILED, $unit IS NOT HEALTHY. Fix $dir by hand now." >&2
  exit 2
fi

# The panel goes second: a new panel can call endpoints that an old binary lacks.
mkdir -p "$dir/static"
put "$release/management.html" "$dir/static/management.html"
echo "$sha $(date -u +%Y-%m-%dT%H:%M:%SZ) $ref" >"$dir/DEPLOYED.tmp-$$"
mv "$dir/DEPLOYED.tmp-$$" "$dir/DEPLOYED"

# Keep the newest 5 releases and drop leftovers of dead builds; the lock rules out a live one.
find "$dir/releases" -maxdepth 1 -name '*.tmp-*' ! -name "*.tmp-$$" -exec rm -rf {} +
find "$dir/releases" -maxdepth 1 -type d -regextype posix-extended -regex '.*/[0-9a-f]{40}' -printf '%T@ %p\n' |
  sort -rn | tail -n +6 | cut -d' ' -f2- | { grep -vF "$release" || true; } | xargs -r rm -rf

echo "deployed ${sha:0:9} ($ref) to $unit"
