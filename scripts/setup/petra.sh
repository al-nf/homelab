#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/../lib/common.sh"
cd -- "$repo_dir"
pi_target="${1:-${RMM_DEPLOY_TARGET:-afung@100.98.188.45}}"
pi_interface="${RMM_PI_INTERFACE:-tailscale0}"
collector_interface="${RMM_COLLECTOR_INTERFACE:-tailscale0}"
if [[ $# -gt 1 || ! "$pi_target" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.@:-]*$ || ! "$pi_interface" =~ ^[a-zA-Z0-9_.-]+$ || ! "$collector_interface" =~ ^[a-zA-Z0-9_.-]+$ ]]; then
  printf 'Usage: ./lab setup petra [user@pi-or-ssh-alias]\n' >&2
  exit 1
fi
if [[ -n "${TMUX:-}" ]]; then
  printf 'Detach from tmux first (Ctrl+B, then D), then run ./lab setup petra.\n' >&2
  exit 1
fi
if [[ "${HOMELAB_DEV_SHELL:-}" != 1 ]]; then
  exec nix --extra-experimental-features 'nix-command flakes' develop "path:$repo_dir" --command bash "$repo_dir/scripts/setup/petra.sh" "$@"
fi
if [[ ! -d "/sys/class/net/$collector_interface" ]]; then
  printf 'Collector interface %s does not exist. Is Tailscale running?\n' "$collector_interface" >&2
  exit 1
fi
printf 'Deploying the dashboard with Tailscale setup support…\n'
bash "$repo_dir/scripts/server/deploy.sh" "$pi_target"
make -C services/rmm-server collector
load_tmux_ref
umask 077
collector_dir="${XDG_CONFIG_HOME:-$HOME/.config}/homelab"
mkdir -p "$collector_dir"
collector_temp="$(mktemp "$collector_dir/.collector.XXXXXXXX")"
trap 'rm -f -- "$collector_temp"' EXIT
printf 'Configuring Tailscale telemetry and preserving existing host tokens…\n'
# Only the private collector JSON goes to stdout, which is redirected into a file.
ssh "$pi_target" "nix --extra-experimental-features 'nix-command flakes' shell '$tmux_ref' --command bash -s -- '$pi_interface' '$collector_interface'" > "$collector_temp" <<'REMOTE'
set -euo pipefail
pi_address="$(ip -4 -o addr show dev "$1" | awk '{print $4}' | cut -d / -f 1 | head -n 1)"
if [[ -z "$pi_address" ]]; then
  printf 'The Pi has no Tailscale IPv4 address on %s.\n' "$1" >&2
  exit 1
fi
cd "$HOME/rmm-server"
./rmm-server -configure-telemetry \
  -telemetry-listen "$pi_address:8081" \
  -collector-target "http://$pi_address:8081" \
  -collector-interface "$2" -link-type tailscale >&2
tmux kill-session -t =homelab 2>/dev/null || true
for attempt in {1..50}; do
  if [[ -z "$(ss -H -ltn 'sport = :8080')" ]]; then break; fi
  sleep 0.1
done
tmux new-session -d -s homelab -n rmm-server -c "$PWD" './rmm-server; read -r'
if ! ./rmm-server -health-check >&2; then
  tmux capture-pane -p -S -80 -t homelab:rmm-server >&2 || true
  exit 1
fi
cat "${XDG_CONFIG_HOME:-$HOME/.config}/homelab/collector.json"
REMOTE
mv -- "$collector_temp" "$collector_dir/collector.json"
printf 'Verifying one real telemetry sample over Tailscale…\n'
if ! ./bin/native/rmm-collector -mode collector -once; then
  printf '\nSample failed. Check tailnet access to the Pi on TCP 8081.\n' >&2
  printf 'Credentials were preserved; the collector was not started.\n' >&2
  exit 1
fi
session="${HOMELAB_SESSION:-homelab}-collector"
if tmux has-session -t "=$session" 2>/dev/null; then tmux kill-session -t "=$session"; fi
./bin/native/rmm-collector -install-collector
printf '\nCollector is running over Tailscale. The dashboard should update within 5 seconds.\n'
printf 'Collector starts automatically at boot. Logs: journalctl --user -u rmm-collector.service -f\n'
