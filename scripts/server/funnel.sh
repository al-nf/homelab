#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/../lib/common.sh"
target="${RMM_DEPLOY_TARGET:-afung@100.98.188.45}"
action="${1:-on}"
usage() {
  printf 'Usage: ./lab funnel [on|status|off]\n'
}
if [[ $# -gt 1 || ! "$target" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.@:-]*$ ]]; then
  usage >&2
  exit 1
fi
case "$action" in
  -h|--help) usage; exit 0 ;;
  on|status|off) ;;
  *) usage >&2; exit 1 ;;
esac
# Configure Funnel on Artemis, forwarding only the dashboard, not telemetry.
exec ssh "$target" "bash -s -- '$action'" <<'REMOTE'
set -euo pipefail
command -v tailscale >/dev/null || { printf 'Tailscale is missing on the server.\n' >&2; exit 1; }
case "$1" in
  on)
    "$HOME/rmm-server/rmm-server" -health-check
    tailscale funnel --bg --https=8443 http://127.0.0.1:8080
    tailscale funnel status
    ;;
  status)
    tailscale funnel status
    ;;
  off)
    tailscale funnel --bg --https=8443 http://127.0.0.1:8080 off
    tailscale funnel status
    ;;
esac
REMOTE
