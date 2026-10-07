#!/usr/bin/env bash
# Real installer in an isolated HOME; service commands are test-only shims.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }
run_case() {
  local mode="$1" workdir bindir shimdir calllog source
  workdir="$(mktemp -d)"
  bindir="$workdir/bin"; shimdir="$workdir/shim"; calllog="$workdir/calls.log"; source="$workdir/source-pi-web"
  mkdir -p "$bindir" "$shimdir"
  printf '#!/usr/bin/env bash\ncase "$1" in -s) echo Linux ;; -m) echo x86_64 ;; esac\n' > "$shimdir/uname"
  for tool in systemctl launchctl pkill sudo; do
    printf '#!/usr/bin/env bash\necho "%s $*" >> "%s"\nexit 0\n' "$tool" "$calllog" > "$shimdir/$tool"
  done
  chmod +x "$shimdir"/*
  : > "$calllog"
  printf '#!/bin/sh\necho v0.0.0\n' > "$bindir/pi-web"
  printf '#!/bin/sh\necho v9.9.9\n' > "$source"
  chmod +x "$bindir/pi-web" "$source"
  local env_vars=("HOME=$workdir" "PI_WEB_INSTALL_DIR=$bindir" "PI_WEB_SOURCE_BINARY=$source" "PATH=$shimdir:/usr/bin:/bin")
  [[ "$mode" == "inplace" ]] && env_vars+=("PI_WEB_INPLACE_UPDATE=1")
  env -i "${env_vars[@]}" bash "$REPO_ROOT/install.sh" </dev/null > "$workdir/out.log" 2>&1 \
    || fail "[$mode] installer failed: $(<"$workdir/out.log")"
  [[ -x "$bindir/pi-web" && -x "$source" ]] || fail "[$mode] installed/source binary missing"
  [[ "$("$bindir/pi-web" -version)" == "v9.9.9" ]] || fail "[$mode] wrong installed version"
  if [[ "$mode" == "inplace" ]]; then
    [[ ! -s "$calllog" ]] || fail "in-place update changed the running service"
  else
    grep -Eq 'systemctl|launchctl|pkill' "$calllog" || fail "normal install did not replace the running instance"
  fi
  echo "ok: $mode"
  rm -rf "$workdir"
}
run_case inplace
run_case normal
echo "PASS: source binary installation and in-place update"
