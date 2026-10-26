#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/../lib/common.sh"
target="${RMM_DEPLOY_TARGET:-afung@100.98.188.45}"
usage() {
  printf 'Usage: ./lab [status|logs|start|stop|restart|ssh|deploy]\n'
}
if [[ $# -gt 1 || ! "$target" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.@:-]*$ ]]; then
  usage >&2
  exit 1
fi
action="${1:-status}"
case "$action" in
  -h|--help) usage; exit 0 ;;
  ssh) exec ssh "$target" ;;
  deploy) exec bash "$repo_dir/scripts/server/deploy.sh" "$target" ;;
  status|logs|start|stop|restart) ;;
  *) usage >&2; exit 1 ;;
esac
# Only Artemis needs tmux/Nix for these commands; the laptop needs just SSH.
load_tmux_ref
exec ssh "$target" "nix --extra-experimental-features 'nix-command flakes' shell '$tmux_ref' --command bash -s -- '$action'" <<'REMOTE'
set -euo pipefail
cd "$HOME/rmm-server"
case "$1" in
  status)
    hostname
    if ! tmux has-session -t =homelab 2>/dev/null; then
      printf 'Dashboard session is stopped. Run ./lab start to start it.\n' >&2
      exit 1
    fi
    ./rmm-server -health-check
    ;;
  logs)
    tmux capture-pane -p -S -200 -t homelab:rmm-server
    ;;
  stop)
    if tmux has-session -t =homelab 2>/dev/null; then
      tmux kill-session -t =homelab
    fi
    ;;
  start|restart)
    if [[ ! -x ./rmm-server ]]; then
      printf 'Dashboard binary missing; run ./lab deploy first.\n' >&2
      exit 1
    fi
    if [[ "$1" == restart ]] && tmux has-session -t =homelab 2>/dev/null; then
      tmux kill-session -t =homelab
    fi
    if ! tmux has-session -t =homelab 2>/dev/null; then
      for attempt in {1..50}; do
        if [[ -z "$(ss -H -ltn 'sport = :8080')" ]]; then break; fi
        sleep 0.1
      done
      if [[ -n "$(ss -H -ltn 'sport = :8080')" ]]; then
        printf 'Port 8080 is occupied; inspect the listener before starting.\n' >&2
        exit 1
      fi
      tmux new-session -d -s homelab -n rmm-server -c "$PWD" './rmm-server; read -r'
    fi
    if ! ./rmm-server -health-check; then
      tmux capture-pane -p -S -80 -t homelab:rmm-server >&2 || true
      exit 1
    fi
    ;;
esac
REMOTE
