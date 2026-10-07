#!/usr/bin/env bash
set -euo pipefail

# pi-web source-fork installer — installs the local build and auto-start.
# From the checkout: node scripts/run-lifecycle.mjs install
# Via Pi: pi install git:github.com/stoneedle/pi-web
# Updates use the same Git source and build the matching binary.

if [[ -n "${PI_WEB_INSTALL_DIR:-}" ]]; then
  INSTALL_DIR="$PI_WEB_INSTALL_DIR"
else
  INSTALL_DIR="${HOME}/.pi/agent/bin"
fi
BINARY="$INSTALL_DIR/pi-web"
SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
VERSION_FILE="${HOME}/.pi/agent/pi-web-version"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${GREEN}→${NC} $*" >&2; }
warn()  { echo -e "${YELLOW}⚠${NC} $*" >&2; }
err()   { echo -e "${RED}✗${NC} $*" >&2; }

# ── Detect platform ─────────────────────────────────────────────────
detect_platform() {
  local os arch
  case "$(uname -s)" in
    Darwin) os="darwin" ;;
    Linux)  os="linux" ;;
    *)
      err "Unsupported OS: $(uname -s)"
      exit 1
      ;;
  esac

  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *)
      err "Unsupported architecture: $(uname -m)"
      exit 1
      ;;
  esac

  echo "${os}-${arch}"
}

# ── Get installed version ───────────────────────────────────────────
installed_version() {
  if [[ -x "$BINARY" ]]; then
    "$BINARY" -version 2>/dev/null || true
  elif [[ -f "$VERSION_FILE" ]]; then
    # Binary not executable yet (e.g., partial install); fall back to version file
    cat "$VERSION_FILE"
  fi
}

# ── Check if update is needed ───────────────────────────────────────
needs_update() {
  local latest="$1"

  if [[ ! -f "$BINARY" ]]; then
    return 0  # not installed yet
  fi

  local installed
  installed="$(installed_version)"
  if [[ -n "$installed" ]] && [[ "$installed" == "$latest" ]]; then
    return 1  # already up-to-date
  fi

  if [[ -n "$installed" ]]; then
    info "Update available: ${installed} → ${latest}"
  else
    info "Existing binary found (unknown version). Installing ${latest}."
  fi

  return 0  # needs update
}

# ── Install binary ──────────────────────────────────────────────────
install_binary() {
  local src="$1"
  local tag="$2"
  local is_update="${3:-false}"
  local inplace="${PI_WEB_INPLACE_UPDATE:-}"

  if [[ -f "$BINARY" ]] && [[ "$is_update" != "true" ]]; then
    # Interactive: ask before overwriting
    warn "pi-web already installed at ${BINARY}"
    read -rp "  Overwrite? [y/N] " answer
    if [[ ! "$answer" =~ ^[Yy]$ ]]; then
      info "Skipping binary install."
      return 1
    fi
  fi

  # Stop running instance before replacing. Skipped for in-place self-updates:
  # pi-web spawned this script (via `pi install`), so stopping the service here
  # would kill the very npm process running it. pi-web triggers its own detached
  # restart afterward (see internal/app/update.go).
  if [[ -f "$BINARY" && -z "$inplace" ]]; then
    if [[ "$(uname -s)" == "Linux" ]]; then
      systemctl --user stop pi-web.service 2>/dev/null || true
    elif [[ "$(uname -s)" == "Darwin" ]]; then
      launchctl unload "${HOME}/Library/LaunchAgents/com.pi-web.plist" 2>/dev/null || true
    fi
    # Also try pkill for manually-started instances
    pkill -f "${BINARY}" 2>/dev/null || true
    sleep 1
  fi

  mkdir -p "$INSTALL_DIR"

  if [[ ! -w "$INSTALL_DIR" ]]; then
    info "Installing to ${INSTALL_DIR} (requires sudo)..."
    sudo cp "$src" "$BINARY"
  elif [[ -n "$inplace" ]]; then
    # Atomic swap so the binary can be replaced while the old process still
    # runs — a plain cp over a running executable fails with ETXTBSY on Linux.
    # The temp file must share $BINARY's directory so the mv is a pure rename(2).
    local staged="${BINARY}.new.$$"
    cp "$src" "$staged"
    chmod +x "$staged"
    mv -f "$staged" "$BINARY"
  else
    cp "$src" "$BINARY"
  fi

  # Record version
  mkdir -p "$(dirname "$VERSION_FILE")"
  echo "$tag" > "$VERSION_FILE"

  info "pi-web ${tag} installed to ${BINARY}"
  return 0
}

# Copy the skill CLI next to the binary so `pi-web-ctl` works from any cwd.
# Missing source (standalone binary-only install) is a no-op — skills ship
# with the npm package, not the GitHub release tarball.
install_ctl() {
  local src="${SRC_DIR}/.pi/skills/common/pi_web.py"
  if [[ ! -f "$src" ]]; then
    return 0
  fi
  mkdir -p "$INSTALL_DIR"
  local dest="${INSTALL_DIR}/pi-web-ctl"
  if [[ ! -w "$INSTALL_DIR" ]]; then
    sudo cp "$src" "$dest"
    sudo chmod +x "$dest"
  else
    cp "$src" "$dest"
    chmod +x "$dest"
  fi
  info "pi-web-ctl installed to ${dest}"
}

# ── Fetch config file from repo (for standalone installs) ──────────
# ── macOS auto-start ─────────────────────────────────────────────────
setup_macos() {
  local plist_dst="${HOME}/Library/LaunchAgents/com.pi-web.plist"
  local needs_reload=true

  mkdir -p "${HOME}/Library/LaunchAgents"

  # Generate plist from local file or fetch from repo
  local generated
  generated="$(mktemp)"
  local plist_src="${SRC_DIR}/init/com.pi-web.plist"
  if [[ -f "$plist_src" ]]; then
    sed "s|/usr/local/bin/pi-web|${BINARY}|g" "$plist_src" > "$generated"
  else
    err "Source checkout is missing init/com.pi-web.plist."
    exit 1
  fi

  info "pi-web will listen on localhost; if Tailscale is running, it will publish HTTPS with Tailscale Serve."

  # Pass the generated environment to launchd. This includes PI_WEB_TOKEN and
  # PATH so pi-web can find `pi` when serving browser chat requests.
  local env_file="${HOME}/.config/pi-web/env"
  if [[ -f "$env_file" ]]; then
    local env_xml=""
    while IFS='=' read -r key value; do
      [[ -z "$key" || "$key" == \#* ]] && continue
      case "$key" in
        PI_WEB_TOKEN|PI_CODING_AGENT_DIR|PATH) ;;
        *) continue ;;
      esac
      value="$(printf '%s' "$value" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g')"
      env_xml="${env_xml}        <key>${key}</key>\n        <string>${value}</string>\n"
    done < "$env_file"

    if [[ -n "$env_xml" ]]; then
      perl -0pi -e "s|</dict>\s*</plist>|    <key>EnvironmentVariables</key>\n    <dict>\n${env_xml}    </dict>\n</dict>\n</plist>|" "$generated"
    fi
  fi

  # Check if plist changed
  if [[ -f "$plist_dst" ]]; then
    if cmp -s "$generated" "$plist_dst"; then
      info "Auto-start config unchanged."
      needs_reload=false
    fi
  fi

  if [[ "$needs_reload" == "true" ]]; then
    cp "$generated" "$plist_dst"
    launchctl bootout "gui/$(id -u)" "$plist_dst" 2>/dev/null || launchctl unload "$plist_dst" 2>/dev/null || true
    launchctl bootstrap "gui/$(id -u)" "$plist_dst" 2>/dev/null || launchctl load "$plist_dst"
    info "macOS auto-start configured (launchd)"
  fi

  rm -f "$generated"

  # Restart if already running
  launchctl kickstart -k "gui/$(id -u)/com.pi-web" 2>/dev/null || {
    launchctl stop com.pi-web 2>/dev/null || true
    launchctl start com.pi-web 2>/dev/null || true
  }
}

# ── Linux auto-start (systemd user service) ──────────────────────────
setup_linux() {
  local service_dir="${HOME}/.config/systemd/user"
  local service_dst="${service_dir}/pi-web.service"
  local needs_reload=true

  mkdir -p "$service_dir"

  # Get service file from local clone or fetch from repo
  local service_src="${SRC_DIR}/init/pi-web.service"
  if [[ ! -f "$service_src" ]]; then
    err "Source checkout is missing init/pi-web.service."
    exit 1
  fi

  local generated_service
  generated_service="$(mktemp)"
  sed "s|/usr/local/bin/pi-web|${BINARY}|g" "$service_src" > "$generated_service"
  info "pi-web will listen on localhost; if Tailscale is running, it will publish HTTPS with Tailscale Serve."

  # Check if service file changed
  if [[ -f "$service_dst" ]]; then
    if cmp -s "$generated_service" "$service_dst"; then
      info "Service config unchanged."
      needs_reload=false
    fi
  fi

  if [[ "$needs_reload" == "true" ]]; then
    cp "$generated_service" "$service_dst"
    systemctl --user daemon-reload 2>/dev/null || {
      warn "Could not reload user systemd; skipping auto-start setup."
      return 0
    }
    info "Linux auto-start updated (systemd user service)"
  fi

  # Enable and restart when user systemd is available.
  systemctl --user enable pi-web.service 2>/dev/null || true
  systemctl --user restart pi-web.service 2>/dev/null || {
    # Service may not be running yet (first install)
    systemctl --user start pi-web.service 2>/dev/null || true
  }
}

# ── Environment setup ────────────────────────────────────────────────
set_env_var() {
  local file="$1"
  local key="$2"
  local value="$3"

  if [[ -f "$file" ]] && grep -q "^${key}=" "$file"; then
    local escaped
    escaped="$(printf '%s' "$value" | sed 's/[&\\]/\\&/g')"
    sed -i.bak "s|^${key}=.*|${key}=${escaped}|" "$file"
    rm -f "${file}.bak"
  else
    printf '%s=%s\n' "$key" "$value" >> "$file"
  fi
}

setup_env() {
  local env_dir="${HOME}/.config/pi-web"
  local env_file="${env_dir}/env"

  mkdir -p "$env_dir"
  chmod 700 "$env_dir" 2>/dev/null || true
  touch "$env_file"
  chmod 600 "$env_file" 2>/dev/null || true

  if [[ -z "${PI_WEB_TOKEN:-}" ]] && ! grep -q '^PI_WEB_TOKEN=' "$env_file"; then
    local token
    if command -v openssl &>/dev/null; then
      token="$(openssl rand -hex 16)"
    else
      token="$(date +%s%N)-$RANDOM-$RANDOM"
    fi

    set_env_var "$env_file" "PI_WEB_TOKEN" "$token"
    info "Generated PI_WEB_TOKEN in ${env_file}"
    warn "Use this token when opening pi-web from another device: ${token}"
  fi

  # Persist PI_CODING_AGENT_DIR so auto-started pi-web finds the right sessions.
  if [[ -n "${PI_CODING_AGENT_DIR:-}" ]]; then
    set_env_var "$env_file" "PI_CODING_AGENT_DIR" "${PI_CODING_AGENT_DIR}"
  fi

  # Services launched by systemd/launchd often have a minimal PATH. Preserve the
  # install-time PATH so pi-web can find `pi` for browser chat (`pi --mode rpc`).
  set_env_var "$env_file" "PATH" "${PATH}"
}

# ── Main ────────────────────────────────────────────────────────────
main() {
  echo ""
  info "pi-web installer"
  echo ""

  local platform
  platform="$(detect_platform)"

  local tag
  local source_binary="${PI_WEB_SOURCE_BINARY:-${SRC_DIR}/pi-web}"
  if [[ ! -x "$source_binary" ]]; then
    err "Build the fork first with make build, or run node scripts/run-lifecycle.mjs install."
    exit 1
  fi
  tag="${PI_WEB_SOURCE_VERSION:-$("$source_binary" -version)}"

  if ! needs_update "$tag"; then
    install_ctl
    info "Already up-to-date (${tag})."
    echo ""
    exit 0
  fi



  # Check if running interactively
  local is_update=false
  if [[ ! -t 0 ]]; then
    is_update=true  # non-interactive → update mode (no prompts)
  fi

  if ! install_binary "$source_binary" "$tag" "$is_update"; then
    # User chose not to overwrite
    exit 0
  fi

  # In-place self-update: pi-web triggered this and restarts itself afterward
  # via its own /api/restart. Skip env/service setup so we don't restart (and
  # kill) the npm process running this script, or clobber the service's PATH.
  if [[ -n "${PI_WEB_INPLACE_UPDATE:-}" ]]; then
    install_ctl
    info "Binary updated to ${tag}; pi-web will restart to apply it."
    echo ""
    exit 0
  fi

  install_ctl
  setup_env

  case "$(uname -s)" in
    Darwin) setup_macos ;;
    Linux)  setup_linux ;;
  esac

  info "Done! pi-web ${tag} is ready."
  echo ""
}

main
