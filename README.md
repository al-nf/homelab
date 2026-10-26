# Homelab

A public RMM dashboard with login protected Wake on LAN controls, live telemetry,
and a network graph showing reported links and interface traffic. Plain HTML, CSS,
and JavaScript, embedded in a Go server. Intended to run on the lab's Raspberry Pi 5.

## Unified command interface

Run `./lab` in a terminal for the split interface: numbered commands on the left,
live output and the last execution's result on the right. Press `1`–`9` or `0`
to select immediately, and `q` to go back or quit. Output follows the latest lines;
there are no scrolling shortcuts. Opening submenus preserves the previous output;
running another command replaces it.

While commands run, typing goes to the command and Ctrl+C interrupts it. SSH and
attached collector sessions temporarily use the full terminal, then return.
The interface requires at least 76 columns by 20 rows and uses Python's standard
curses library. If Python/curses is unavailable, the launcher supplies it using Nix.

Direct commands remain available with `./lab --help`. For example,
`./lab --target afung@artemis status` checks another SSH destination.
With redirected input/output, `./lab` prints help instead of opening a menu.

Implementations live in `scripts/server/`, `scripts/local/`, and `scripts/setup/`,
with shared helpers in `scripts/lib/`. Use `./lab` for all commands; the old
compatibility aliases have been removed. The generic `./lab setup laptop` retains
the older manual background collector; use `./lab setup babylon` for its LaunchAgent.

Generated executables live under `bin/`: `native/` for local collector builds,
`linux-arm64/` for Artemis, and `darwin-arm64/` or `darwin-amd64/` for Mac
collectors. Collector setup rebuilds its executable when needed. Generated
executables are ignored by Git.

## Run on Linux ARM64

The dashboard server runs only on Linux ARM64 (aarch64), including Artemis.
On Babylon or Petra, use `./lab deploy` to build and update Artemis remotely.
`make build` always cross-compiles the Linux ARM64 server; `make collector`
builds a native collector for the current machine.

```sh
./lab run
```

`run` enters a plain pinned Nix shell, builds `services/rmm-server`, starts the
server in tmux, and attaches. Run it again to reattach. Supports Linux aarch64, including 64-bit Raspberry Pi OS. Nothing is installed or started
through systemd by the dashboard mode of `run`. The older projects are not launched.

Detach with Ctrl+B, then D; the dashboard continues after SSH disconnects. The
session is named `homelab` (override with `HOMELAB_SESSION`). Inside another tmux
session, `run` switches clients rather than nesting tmux. A failed server leaves
its window open so you can read the error.

Stop the session and dashboard with:

```sh
tmux kill-session -t homelab
```

To rebuild after edits, detach and run `./lab run --restart`. Use `./lab run --detach` to
start without attaching. After collector setup, `./lab collector` starts the
installed user service and follows its journal; `--restart` rebuilds, updates the
installed binary, and restarts it. Without an installed service, collector mode
retains the original tmux workflow.

## Development and login setup

Run tests on your development machine. Run `./configure-login` on Linux ARM64
where the dashboard is hosted.

```sh
nix --extra-experimental-features 'nix-command flakes' develop path:.
cd services/rmm-server
make test
make build
./configure-login
```

Restart the dashboard after configuring its password. See the
[RMM server guide](services/rmm-server/README.md) for login, saved sessions,
network configuration, and public HTTPS access through Tailscale Funnel.

## Migration from devenv

`flake.nix` is a small `mkShell` declaration containing Go, Make, tmux, rsync, SSH, and Python.
`flake.lock` preserves the nixpkgs revision previously used by devenv. There are no
devenv modules, dotenv evaluation, or direnv requirements. Entering the directory
does not build a shell; use `./lab run` or `nix --extra-experimental-features 'nix-command flakes' develop path:.` explicitly. Run
`direnv allow` once if you still use direnv, so it accepts the replacement `.envrc`.

The service moved from `services/wake-on-lan` to `services/rmm-server`; its module
and binary are now named `rmm-server`. Saved credentials and sessions still use the
`homelab` configuration/state directories, so the rename does not reset them.
An old `dev` tmux session is left alone; stop it yourself when switching over to
avoid running two copies. Tailscale's existing proxy can keep forwarding to 8080.

## Control artemis from babylon

From this checkout on the M3 Mac, with Tailscale connected:

```sh
./lab status
./lab logs
./lab restart
./lab ssh
./lab deploy
```

`status` checks the remote tmux session and dashboard health. `logs`
prints the last 200 lines of dashboard output. `start`, `stop`, and `restart`
manage the dashboard's `homelab` tmux session. `ssh` opens a shell on artemis;
`deploy` builds on this laptop, uploads, and restarts the dashboard. These commands
target `afung@100.98.188.45`; set `RMM_DEPLOY_TARGET` to override it. Tailscale SSH
may print an authentication link that you need to open before the command proceeds.

Only deployment needs local Nix; the other controls use macOS's built-in SSH.
The pinned development shell supports Apple Silicon macOS as well as Linux.
Petra does not need to be online to control or deploy to artemis from babylon.
Use the dashboard's existing HTTPS URL for login and Wake on LAN controls.
`./lab setup babylon` is the collector installer run from petra, and is not needed
to control artemis from this laptop.

## Public HTTPS access

From Babylon, run `./lab funnel` (or `./lab funnel on`) to enable Tailscale
Funnel on Artemis. The script checks
server health, forwards public HTTPS port 8443 to `http://127.0.0.1:8080`, and
prints the HTTPS URL. It persists across SSH disconnects and server reboots.

Use `./lab funnel status` to show the URL/configuration and `./lab funnel off`
to remove this dashboard endpoint. Other Funnel ports are left alone.
Telemetry port 8081 remains private. The dashboard overview is public; wake
controls require dashboard login. Use the printed HTTPS URL, including `:8443`.

Artemis must have Tailscale installed and permission for this user to manage
Funnel. If Tailscale prints a setup link, follow it to enable HTTPS/Funnel for
the tailnet. See the [Tailscale Funnel CLI documentation](https://tailscale.com/docs/reference/tailscale-cli/funnel).

## Deploy to the Pi while developing

```sh
./lab deploy
```

The default SSH destination is `afung@100.98.188.45` (artemis's reported Tailscale
IP). Override it with `./lab deploy afung@artemis` or `RMM_DEPLOY_TARGET`. The scripts
explicitly enable the `nix-command` and `flakes` experimental features for local
and remote Nix invocations; no global Nix setting changes are needed.

Deployment cross-compiles one static ARM64 binary at `./bin/linux-arm64/rmm-server`, copies it to
`~/rmm-server/rmm-server` on the Pi with rsync, and restarts the `homelab` tmux
session. It checks that the new binary responds on port 8080. The Pi uses the
repo's pinned nixpkgs version to supply tmux. There are no source uploads, release
directories, deployment snapshots, or tests in the deployment command.

Run `make test` or `make test-race` in `services/rmm-server` separately when needed.
The race detector requires a compatible memory layout and cannot run on a Pi using
39-bit VMA. Local builds use `-buildvcs=false`, so Git stamping is not required.

Passwords, telemetry tokens, and saved sessions stay in their existing configuration
and state directories on the Pi. The existing Tailscale Serve/Funnel proxy continues
to use port 8080. Restart the local collector separately with
`./lab collector --restart` after collector changes.

See the [collector setup guide](services/rmm-server/README.md#telemetry-over-tailscale)
for the one-time Pi receiver and petra collector configuration.

## First collector on petra

From this repo on petra, outside tmux:

```sh
./lab setup petra
```

Setup deploys the latest binary, configures the Pi's receiver on its Tailscale IPv4
address and TCP 8081, copies matching credentials into a private local file, and
verifies one sample before installing and starting `rmm-collector.service` as a
systemd user service. It enables lingering so the collector starts at boot and
survives logout, and restarts it automatically if it exits. Existing
Ethernet telemetry configuration is migrated; host tokens and login sessions are
preserved. The old local collector tmux session is stopped during migration.
No network configuration changes are made.

Both endpoints use `tailscale0` by default. Override the SSH destination with
`./lab setup petra afung@artemis`. `RMM_PI_INTERFACE` and
`RMM_COLLECTOR_INTERFACE` override interface names if necessary. View logs with
`journalctl --user -u rmm-collector.service -f`. If enabling lingering requires
administrator authentication, run `sudo loginctl enable-linger afung` once and
rerun setup. The installed executable lives at
`~/.local/share/homelab/collector/rmm-collector` and does not need the development
shell at boot. Stop/start it with `systemctl --user stop/start rmm-collector.service`.

Tailscale chooses the underlying network path, which can be direct Ethernet,
Wi-Fi, or a relay. `tailscale ping artemis` reports the chosen path. The graph
labels the overlay as Tailscale and does not invent a negotiated physical speed.
RX/TX rates describe the selected interface's total traffic, not just this peer.
TCP 8081 must be allowed between the devices by tailnet policy and host firewalls.
No Funnel/Serve configuration is needed for ingestion; public HTTPS still forwards
only dashboard port 8080. WoL retains its existing configured target MAC.

## Deployment status and connection details

The graph hub uses the dashboard host's actual lowercase hostname (`artemis` on the
Pi). Machine nodes use the collector's lowercase hostname when reported, falling
back to their configured name until then; configured IDs remain stable for WoL.

Click an edge or its speed label to inspect negotiated speed, interface, receive
and transmit rates, byte totals, carrier, and the last sample. Rates are computed
from consecutive byte counters at the Pi. First samples, counter resets, interface
changes, and stale intervals produce unavailable rates rather than invented zeros.
Receive/transmit directions are labeled from the reporting machine's perspective.

A deployment strip shows the running hostname, startup time, and uptime.
The public `/api/health` response also includes the binary fingerprint used
by deployment readiness checks.

## Telemetry history

The History section shows CPU usage, RAM usage, and CPU temperature for each
machine, including artemis. Choose a device or select its graph node, then switch
between the last hour and last 24 hours. Hover a chart or focus it and use the
arrow keys to inspect values. Historical charts refresh every 30 seconds.

Artemis stores history in `~/.local/state/homelab/history.sqlite`, alongside its
session state, so binary deployments and collector restarts preserve it. The
database retains 48 hours, with at most one reading per device per five seconds.
Charts show 15-second averages in the hour view and two-minute averages in the day
view. Missing readings remain null and empty intervals appear as gaps. No data
is backfilled before the first deployment of this feature.

`HOMELAB_STATE_DIR`/`XDG_STATE_HOME` also apply to the history database;
`-history-db /absolute/path/history.sqlite` overrides its location. SQLite's WAL
and shared-memory sidecar files belong with the database when backing it up while
running. History uses WAL with normal synchronization: it survives ordinary
restarts, while recent commits may be lost after a power failure. Storage errors
are logged and shown in the History section; live readings and wake controls
continue to work.

SQLite is compiled into the binary using a Go driver; no SQLite installation is
needed on the Pi or collectors. Builds require Go 1.26+, supplied by the pinned
Nix shell. Only `./lab deploy` is needed to enable history; collectors already send
the required readings.

## Add the macOS laptop (babylon)

From this repo on petra, run:

```sh
./lab setup babylon
```

This script is specifically for **babylon (M3)** and artemis. It connects to
`afung@100.119.223.118` and `afung@100.98.188.45` using their stable Tailscale
addresses. It builds the macOS binary locally, registers babylon on artemis, and
copies the binary and private token to `~/rmm-collector` on babylon. No Go, Nix,
Homebrew, or sensor helper is needed on the Mac. SSH Remote Login must be enabled.
The setup command takes no arguments; it does not rely on MagicDNS resolution.
`RMM_DEPLOY_TARGET` can override the server destination.
Mac SSH commands and rsync transfers share one authenticated connection during
setup, avoiding repeated password prompts. The connection closes when setup exits.

The script verifies a real sample, then installs a user LaunchAgent at
`~/Library/LaunchAgents/local.homelab.collector.plist`. It starts at macOS desktop
login, survives SSH disconnects, and restarts automatically if it exits. Re-run
setup to update it; no setup is needed after rebooting and logging in.
Sleeping or disconnected laptops become stale after 30 seconds and resume reporting
when awake. Logs are at `~/rmm-collector/collector.log`. Babylon must be logged into
the macOS desktop when setup installs its LaunchAgent. Existing manual background
collectors are stopped during migration.

macOS reports CPU usage, RAM, and Tailscale interface traffic using built-in
`top`, `vm_stat`, `sysctl`, and `netstat`. RAM usage excludes free, inactive, and
speculative pages; it may differ from Activity Monitor. CPU temperature comes
directly from Apple's IOKit/AppleSMC interface inside the same binary. It reports
the hottest valid CPU sensor (excluding battery, ambient, and GPU sensors),
matching the Linux collector's convention. The first successful read is logged
during setup; sensor failures are logged and leave temperature unavailable.
GPU/VRAM remain unavailable. The collector uses no sudo or external sensor tools.
The build uses the pinned Go library `purego` to call the system framework without
Cgo; it adds no separate runtime executable or installation step on babylon.

## Adding more machines

Hosts and collector credentials are keyed by stable host IDs, not by the cable or
IP address. Each collector has its own `target`, `interface`, token, and optional
`link_type` (`ethernet`, `wifi`, `tailscale`, `other`). The graph and details carry
this type instead of assuming every machine uses Ethernet. Interfaces with a
`wireless` directory are detected as Wi-Fi; Tailscale interfaces are recognized.
Unreadable link speed stays unavailable rather than being assigned a wired rate.

`setup-petra` bootstraps petra over Tailscale; `setup-babylon` sets up babylon.
The Mac uses `interface: "auto"` to follow the route's interface, including
macOS's dynamically named `utun` devices. The collector rechecks it each sample so
Tailscale can reconnect or start after login. Registered hosts are saved privately in
`~/.config/homelab/hosts.json` on the Pi, retaining petra's WoL configuration.
`HOMELAB_HOSTS_FILE` overrides this file. Machines without a MAC have no wake button.

## TODO

- Add alternate receiver listeners and wireless link speed collection.
- Bring desk-cube / pingme functionality from `projects/` into the dashboard as features.
- Integrate Discord and other notification controls into the dashboard.
