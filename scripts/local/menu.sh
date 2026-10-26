#!/usr/bin/env bash
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/../lib/common.sh"
if [[ ! -t 0 || ! -t 1 ]]; then
  printf 'The menu needs an interactive terminal. Use ./lab --help for commands.\n' >&2
  exit 1
fi
if ! command -v python3 >/dev/null || ! python3 -c 'import curses' 2>/dev/null; then
  exec nix --extra-experimental-features 'nix-command flakes' develop "path:$repo_dir" --command python3 "$repo_dir/scripts/local/menu.py"
fi
exec python3 "$repo_dir/scripts/local/menu.py"
