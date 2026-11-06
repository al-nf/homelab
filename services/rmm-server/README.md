# RMM server

A public, dark dashboard with authenticated Wake on LAN and suspend controls. The existing
Ethernet MAC belongs to **petra**, the workstation, and remains the default target.
A collector on each machine reports CPU, RAM, temperatures, GPU/VRAM, and Ethernet
link speed. Unavailable readings stay null. Stale telemetry does not establish
power state, and a wake response only confirms packet transmission.

## Suspend petra

Run `./lab setup petra` from petra to update the dashboard and collector and enable
the **Suspend** button beside **Wake**. Login is required for both actions.
Suspend stays disabled unless a recent collector report advertises support.

The collector runs the fixed Nushell expression `suspend`, explicitly loading
`~/.config/nushell/config.nu` (or `$XDG_CONFIG_HOME/nushell/config.nu`). This preserves
petra's existing lock-before-sleep behavior. The active Wayland environment is
read from the systemd user manager when the action runs; no SSH keys or extra
listening port are needed. The collector user must have Nushell and the existing
suspend script installed, with permission to suspend the machine.

Before requesting sleep, the collector ends its user's incoming OpenSSH login
sessions through `loginctl terminate-session`. It checks each session's owner,
remote flag, and `sshd` service first. This logs out those sessions and ends
processes attached to them. The desktop, collector service, and SSH listener keep
running, so new SSH connections work after wake. Other users' sessions and
outgoing SSH connections are unaffected. If SSH-session cleanup fails, suspend
is stopped and the error is reported.

If petra's script fails because `petra-suspend.service` already exists, the
collector checks `hyprctl locked`. Only an explicit `true` permits it to request
sleep through systemd while leaving the existing locker running. Unknown or
unlocked state stops the action. The collector never stops or restarts the locker.

The private collector configuration opts in with `"allow_suspend": true`; other
collectors remain disabled by default. Requests arrive in the next telemetry
response, normally within five seconds. They expire after 15 seconds, are delivered
once, and are limited to one request per 30 seconds per host. The browser confirms
that the request was queued, not that the machine slept. Execution failures appear
under the device's buttons after its next report, with details in
`journalctl --user -u rmm-collector.service` on petra. Pending requests are not
persisted across dashboard restarts. Single-sample (`-once`) runs never accept actions.

## Run on Linux ARM64

The dashboard server runs only on Linux ARM64. Collectors support Linux and macOS.
Build and deploy from Babylon with `./lab deploy` at the repository root.

From the repository root, enter the pinned Nix toolchain:

```sh
nix --extra-experimental-features 'nix-command flakes' develop path:.
cd services/rmm-server
go run .
```

Open <http://localhost:8080>. Without a saved password or `HOMELAB_PASSWORD`, the
dashboard works in view-only mode. To try login over local HTTP, restart with:

```sh
read -rsp 'Dashboard password (at least 16 characters): ' HOMELAB_PASSWORD
printf '\n'
export HOMELAB_PASSWORD
HOMELAB_INSECURE_COOKIES=1 go run .
```

The password is not stored in the repository. Sessions last **90 days**, including
browser and dashboard restarts. Logout revokes the current session. Login attempts are
limited to 10 per minute across the service; wake requests to one per 10 seconds
per machine. A public login flood can temporarily block legitimate logins.

## Set up login and a persistent service

From the repository root, run `nix --extra-experimental-features 'nix-command flakes' develop path:.`, then `cd services/rmm-server`:

```sh
make build
./configure-login
```

The setup command asks for a password twice without displaying it, then stores it
in `~/.config/homelab/password` (or `$XDG_CONFIG_HOME/homelab/password`) with mode
0600. The standalone `rmm-server -set-password` command also prompts with echo
hidden and finishes on Enter. You can also pipe a password to `../../bin/linux-arm64/rmm-server -set-password`. Restart the
dashboard after setting or changing its password. `HOMELAB_PASSWORD`, if set,
overrides the saved password; unset it to use the setup command's password.

The normal deployment flow is the root `./lab run` script, which starts the dashboard
in tmux. No systemd setup is needed. An optional user service installer is
available if you later choose to manage only the dashboard that way. Run it on the
intended host, after stopping any existing copy:

```sh
./install-service
loginctl enable-linger "$USER"
```

The service runs the compiled binary from this checkout. Linger starts the user
service manager at boot and keeps it running after SSH logout. Tailscale Serve or
Funnel configuration created with `--bg` continues to forward to port 8080. Open
your existing HTTPS URL and log in. Cookie credentials are never stored in browser
local storage. Cookies are Secure, HttpOnly, and valid for 90 days; clearing browser
cookies or switching browsers requires another login.

Session token hashes are stored in `~/.local/state/homelab/sessions.json` (honoring
`XDG_STATE_HOME` or `HOMELAB_STATE_DIR`) with mode 0600. Password changes invalidate
all sessions on restart. Logout remains effective after restart. To revoke all
sessions manually, stop the service, delete `sessions.json`, and restart.

After code changes, rebuild with `make build`, then run:

```sh
systemctl --user restart rmm-server
journalctl --user -u rmm-server -n 30 --no-pager
```

## Internet access with Tailscale Funnel

From this repository on Babylon, use `./lab funnel on` to configure Artemis,
`./lab funnel status` to show the public URL, and `./lab funnel off` to remove
the dashboard endpoint. The following commands are the equivalent manual setup on Artemis.

Run the service on an always-on Linux host on the target's Ethernet broadcast
network. A service running on petra cannot wake petra while petra is powered off.
Tailscale carries browser traffic to the dashboard; wake packets originate from
the host running this service, not the browser or Tailscale network.

Set `HOMELAB_PASSWORD` as above, then run **without** the local HTTP cookie override:

```sh
unset HOMELAB_INSECURE_COOKIES
go run .
```

In another terminal on the service host:

```sh
tailscale funnel --bg --https=8443 http://127.0.0.1:8080
tailscale funnel status
```

Visit the HTTPS URL printed by Tailscale, including when testing login locally.
Funnel requires the appropriate tailnet policy and HTTPS configuration; follow
the setup prompts if requested. No router port forwarding is needed. See the
[official Funnel documentation](https://tailscale.com/docs/reference/tailscale-cli/funnel).
The entire overview is public, including machine names, descriptions, and future
telemetry. MAC and broadcast addresses are excluded from the public API.

To stop exposing the dashboard endpoint:

```sh
tailscale funnel --bg --https=8443 http://127.0.0.1:8080 off
```

The service binds to loopback by default. Keep it there behind Funnel. Secure,
HttpOnly, SameSite cookies are enabled by default. Mutations require an authenticated
session (except login/logout), the `X-Homelab-Request: 1` header, and a matching
Origin when supplied. Cross-origin browser requests are not permitted.

## Configure machines

Copy `hosts.example.json`, customize it, and set `HOMELAB_HOSTS_FILE` to its path.
Without an override, `~/.config/homelab/hosts.json` is used if present; otherwise
the server uses petra's original MAC. All addresses are configured server side;
visitors cannot submit arbitrary MAC addresses. Broadcast must be an IPv4 address
and UDP port. Use the LAN's directed broadcast address if the global broadcast
address does not reach the desired interface. Firmware/NIC Wake on LAN must be enabled.

Telemetry-only machines can omit both `mac` and `broadcast`. Their graph nodes and
metric cards work normally, and no wake button is shown.

| Variable | Default | Purpose |
| --- | --- | --- |
| `HOMELAB_PASSWORD` | unset | Operator password, at least 16 characters; overrides the saved password |
| `HOMELAB_PASSWORD_FILE` | `$XDG_CONFIG_HOME/homelab/password` | Saved password path; used when the environment password is unset |
| `HOMELAB_STATE_DIR` | `$XDG_STATE_HOME/homelab` | Persistent session directory; defaults to `~/.local/state/homelab` |
| `HOMELAB_HOSTS_FILE` | config directory's `homelab/hosts.json`, if present | Path to host JSON configuration |
| `HOMELAB_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `HOMELAB_INSECURE_COOKIES` | unset | Set `1` only for local HTTP login testing |

```sh
make test
make build
../../bin/linux-arm64/rmm-server
```

Assets are embedded at build time. Restart `go run .` after edits to refresh the
preview. The root `./lab run` builds from current source when creating its tmux session.

## Dashboard API

- `GET /api/hosts`: public stable host IDs, lowercase hostnames, descriptions, nullable telemetry, and stale state.
- `GET /api/health`: running build fingerprint, hostname, release ID, start time, and uptime.
- `GET /api/hosts/{id}/history?range=1h|24h`: public CPU, RAM, CPU temperature, and per-processor power history; `__controller__` selects the dashboard host. The range defaults to `1h`.
- `GET /api/session`: current login state and whether login is configured.
- `POST /api/hosts/{id}/suspend`: authenticated, CSRF-protected request to suspend a connected, opted-in collector.
- `POST /api/login`: JSON `{ "password": "…" }`; establishes a session cookie.
- `POST /api/logout`: revokes the session and clears the cookie.
- `POST /api/hosts/{id}/wake`: authenticated wake packet for a configured target.
- `POST /wake`: authenticated compatibility endpoint for the first configured host.

All POST requests require `X-Homelab-Request: 1`. A successful wake response only
confirms packet transmission. It does not establish that the target is online.

## Telemetry over Tailscale

Artemis automatically collects its own CPU usage, RAM usage, and CPU temperature
every five seconds while the dashboard runs. Click the center node to select its
telemetry card. No separate collector process or credentials are needed on artemis.
Deploy an updated binary with `./lab deploy` to enable this on the Pi.

The dashboard runs on artemis; petra runs the collector. From the repo root on
petra, run `./lab setup petra` outside tmux. This configures the receiver, migrates
existing Ethernet telemetry settings, copies credentials privately, verifies a
real sample, and installs the collector as `rmm-collector.service` for the current
user. It starts at boot with lingering, survives logout, and restarts automatically.
Tokens are preserved. Logs: `journalctl --user -u rmm-collector.service -f`.

For the macOS laptop, run `./lab setup babylon` on petra. This builds and installs a
standalone collector on `afung@babylon`, adds a telemetry-only host, and verifies
a real sample before enabling a LaunchAgent. It starts at macOS desktop login and
restarts automatically; no setup is needed after rebooting and logging in.
See the root README for destinations, logs, and metric limitations.

Telemetry targets the Pi's Tailscale IPv4 address on TCP 8081. The receiver binds
only that address. Keep this port out of public Funnel/Serve configuration; those
continue to forward only to the dashboard on `127.0.0.1:8080`. Tailnet policy and
host firewalls must permit petra to connect to artemis on TCP 8081. The ingestion
token is separate from browser login credentials.

The graph labels this as a Tailscale connection. It shows RX/TX rates measured
from the virtual interface's byte counters, across all peers using that interface.
No physical negotiated speed is assigned to a virtual link. Tailscale chooses the
underlying route; check `tailscale ping artemis` for direct/relayed connectivity.
See [Tailscale connection types](https://tailscale.com/docs/reference/connection-types).

Live readings refresh every 5 seconds and become stale after 30 seconds. The
latest snapshots are in memory; CPU, RAM, CPU temperature, and per-processor power history is persisted
to SQLite in the dashboard state directory. Missing samples do not establish that
a host is powered off. Hostnames are
reported by each collector and displayed in lowercase. Rates require two fresh,
consecutive samples; counter resets or interface changes clear them.

On Linux, the collector uses `/proc/stat`, `/proc/meminfo`, CPU thermal/hwmon sensors,
`nvidia-smi`, and interface counters. Unavailable metrics stay null. GPU reporting
currently targets NVIDIA and requires its driver/tools on the reporting machine.
On macOS, built-in `top`, `vm_stat`, `sysctl`, and `netstat` provide CPU, RAM, and
interface counters. RAM excludes free, inactive, and speculative pages. CPU
temperature on babylon's M3 is read directly through IOKit/AppleSMC, using the
hottest valid CPU sensor. No sensor helper or sudo is required; unavailable or
invalid sensor readings stay null with a diagnostic in the collector log.
The `purego` library is compiled into the same executable to call Apple's system
framework. GPU utilization, temperature, and VRAM metrics remain null on macOS. `interface: "auto"` selects the interface used to
reach the receiver on each sample, avoiding a hardcoded macOS `utun` number.

### Processor power

Cards and history display CPU/GPU watts and supported PMIC rail readings:

- Linux CPU: package-level RAPL `energy_uj` deltas, averaged over the sampling
  interval. Child core/DRAM counters are excluded to avoid double counting.
- NVIDIA GPU: `nvidia-smi` board power, including the board's memory and other
  components. Unsupported sensors remain unavailable.
- babylon M3: native IOReport Energy Model CPU and GPU counters, averaged over
  the sampling interval. No extra executable, Homebrew package, or sudo is used.
- artemis (Pi 5): `vcgencmd pmic_read_adc` supplies voltage and current for the
  twelve PMIC output rails. The collector sums each rail's volts × amps for
  **Measured rails**, and separately shows **CPU/GPU core** from `VDD_CORE`.
  The shared core rail is already included in Measured rails. It cannot be split
  into separate CPU and GPU watts. Both readings have live rows and history charts.

These scopes differ; the readings are not whole-machine or wall-socket power.
Pi PMIC output readings exclude conversion losses and USB or other loads connected
straight to 5 V. Measured rails stays unavailable if any required rail reading is
missing or invalid, rather than showing a misleading partial sum. The source is
enabled only when the device-tree model identifies a Pi 5; older Pis do not expose
these ADCs. See the [Raspberry Pi PMIC ADC documentation](https://www.raspberrypi.com/documentation/computers/os.html#pmic_read_adc-channel).

Artemis collects inside the dashboard process: `./lab deploy` updates and restarts
it, with no separate collector setup. `vcgencmd` must be installed and callable by
the dashboard user without sudo. If power stays blank, run `vcgencmd pmic_read_adc`
as that same user on artemis and check `./lab logs` for the diagnostic. Older Pi OS
installations with the tool under `/opt/vc/bin/` are also supported. Read failures
are retried every sample and logged only when the error changes or recovers.

A zero means an actual zero reading, while unavailable sensors and interrupted intervals are
blank. Existing history remains intact; power history starts with updated
collectors. The public history response adds `processors` metadata and each
point's `power_watts` map, keyed by processor ID.

Petra's RAPL energy files are root-readable by default. Keep the collector running
as your user; grant it read access only to the package energy files if desired.
An administrator can assign a dedicated group to those specific sysfs files with
`chgrp` and grant that group read access with `chmod g+r`, then add the collector
user to that group. Reapply those file permissions at boot or after a driver
reload with a persistent NixOS rule. No permission changes
are made by the setup scripts. Without access the CPU wattage stays blank;
other metrics continue to report. Linux powercap ABI documentation:
[energy counters and package zones](https://docs.kernel.org/power/powercap/powercap.html).
Mac bindings follow the native IOReport calls used in
[macmon's source](https://github.com/vladkens/macmon/blob/main/src/sources.rs);
NVIDIA field semantics are in the
[nvidia-smi documentation](https://docs.nvidia.com/deploy/nvidia-smi/index.html).

Update the dashboard before the collectors: run `./lab setup petra`, then
`./lab setup babylon` from this repository on petra. Each setup deploys the dashboard
before sending the new telemetry format, and restarts its installed collector.
`./lab deploy` alone updates artemis and the dashboard, but not remote collectors.

To build a native collector and inspect one local sample without sending it,
from the repository root:

```sh
make -C services/rmm-server collector
./bin/native/rmm-collector -mode sample
```

To send one sample using the saved collector configuration:

```sh
./bin/native/rmm-collector -mode collector -once
```

`./lab collector --restart` rebuilds/restarts the collector. The private files
`~/.config/homelab/telemetry.json` and `collector.json` honor `XDG_CONFIG_HOME`.
`-telemetry-config` and `-collector-config` override them. For custom configurations,
`-configure-telemetry` updates the listener/target while retaining per-host tokens;
`-collector-interface` and `-link-type` select the reporting interface and transport.
`-init-telemetry` remains available for new configurations and refuses overwrites.

For an already configured machine, `-install-collector` installs/enables autostart
without reconfiguring its receiver or rotating credentials. Linux uses a user
service and a binary copied to the XDG data directory; macOS uses a LaunchAgent
pointing to the installed collector binary. Linux boot startup requires lingering;
macOS startup is at desktop login. Both managers restart failed collectors.

## History storage

The dashboard writes `history.sqlite` beside `sessions.json`, honoring
`HOMELAB_STATE_DIR` and `XDG_STATE_HOME`. `-history-db` overrides the database path.
The driver is compiled into the executable, so deployments still upload one
binary. The Go 1.26+ toolchain is needed only when building.

History uses server receive timestamps and stores host IDs, the chart metrics, and processor IDs, names, kinds,
measurement scopes, and watts. Tokens, credentials, MAC addresses, and raw collector payloads are
not stored. One sample is retained per five-second bucket per host, with the
newest report winning. Old records are pruned at startup and hourly, retaining
48 hours. Index pages are reused as old data is removed.

Hour queries return at most 241 points averaged over 15-second buckets; day
queries return at most 721 points averaged over two-minute buckets. Null metrics
and empty buckets remain null. Charts do not connect across empty buckets.
Arbitrary ranges and unknown devices are rejected. Queries have a three-second
timeout. History remains public like current telemetry.

Storage failures do not prevent the dashboard from showing live telemetry or
sending authenticated wake packets. The History section reports unavailability.
WAL mode with normal synchronization preserves history across ordinary restarts;
recent commits may be lost after sudden power loss. Use SQLite's backup API or
stop the dashboard before copying the database without its WAL sidecars.
