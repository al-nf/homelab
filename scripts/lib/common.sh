#!/usr/bin/env bash
# Shared by scripts one directory below scripts/.
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"

load_tmux_ref() {
  local nixpkgs_rev
  nixpkgs_rev="$(awk -F '"' '/"rev"/ { print $4; exit }' "$repo_dir/flake.lock")"
  if [[ ! "$nixpkgs_rev" =~ ^[a-f0-9]{40}$ ]]; then
    printf 'Cannot read the nixpkgs revision from flake.lock.\n' >&2
    return 1
  fi
  tmux_ref="github:NixOS/nixpkgs/$nixpkgs_rev#tmux"
}
