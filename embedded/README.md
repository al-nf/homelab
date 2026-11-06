# Embedded development environment

This x86_64 Linux flake provides a shared FHS shell for Zynq-7000 development,
Yocto Scarthgap (5.0), and AMD Vivado/Vitis 2025.2. The target board is the
**Digilent Cora Z7-07S** (XC7Z007S, one Cortex-A9 core, 512 MiB DDR3).
Vivado itself is proprietary and must be
downloaded and installed separately. The FHS shell supplies conventional Linux
library paths on NixOS; it does not change the host's distribution identity.

The flake and its launcher script live in `nix/`, so Nix only copies that small
directory into the store. Yocto sources, builds, and caches remain outside the
flake source.

## Enter the environment

Install and enable `direnv` (preferably with `nix-direnv`), then run:

```sh
direnv allow
embedded-fhs
```

Without direnv, use `nix develop path:./nix` followed by `embedded-fhs`. Run Yocto's
`oe-init-build-env` **inside** the FHS shell, because it changes the current
shell's environment and directory:

```sh
embedded-fhs
source /path/to/poky/oe-init-build-env /path/to/build
bitbake core-image-minimal
```

The shell includes GCC 13, Python 3.12 with Yocto's Python dependencies, kas,
device-tree compiler, U-Boot image tools, OpenOCD, GDB, an ARM bare-metal
toolchain (`arm-none-eabi-gcc`), CMake, Ninja, and picocom. The FHS profile sets
the UTF-8 locale and clears inherited compiler settings before BitBake runs.

## Managed Yocto workspace

Run these from this directory. kas checks out the exact revisions in `kas/`
under `sources/` and generates `build/conf/`. Downloads and shared-state objects
stay in `downloads/` and `sstate-cache/`. These generated directories are ignored.
The wrapper's first argument is the workspace directory; subsequent arguments
are passed to kas. Keep Yocto sessions separate from shells where you have
sourced Vivado/Vitis settings or an SDK.

Start with an ARM QEMU build to exercise the Yocto host setup:

```sh
KAS_BUILD_DIR="$PWD/build-qemu" yocto-fhs . build kas/qemu.yml
KAS_BUILD_DIR="$PWD/build-qemu" yocto-fhs . shell kas/qemu.yml -c 'runqemu qemuarm nographic slirp'
```

For the AMD Zynq-7000 software baseline:

```sh
yocto-fhs . shell kas/zynq.yml -c 'bitbake -p'
yocto-fhs . build kas/zynq.yml
yocto-fhs . shell kas/zynq.yml
# Inside that shell, generate a Linux application SDK:
bitbake core-image-minimal -c populate_sdk
```

The Zynq configuration pins AMD's `rel-v2025.2` layer plus its Scarthgap
dependencies. It builds kernel/rootfs software artifacts with SD-image and
boot-firmware packaging disabled. **It is not a Cora BSP or a bootable SD image.**
The selected AMD layer has no Cora machine definition. Before booting hardware,
add a project layer containing a Cora-specific machine derived from
`zynq-generic`, with its matching device tree and boot configuration.

Export the hardware handoff (`.xsa`) from your Cora Z7-07S Vivado design and keep
it under `hardware/`. Generate the matching FSBL and device tree using a flow
compatible with 2025.2, and package the FSBL, optional bitstream, and U-Boot into
`BOOT.BIN`. The device tree must describe the 07S's single core, DDR, UART,
Ethernet PHY, and your PL peripherals. Restore board-specific `IMAGE_BOOT_FILES`,
`EXTRA_IMAGEDEPENDS`, and an SD image type in that machine/profile when those
inputs exist. A different Zynq board's boot files or DDR initialization are not
interchangeable with the Cora's.

Use the generated Yocto SDK for Linux applications: install the script under
`build/tmp/deploy/sdk/`, enter a fresh `embedded-fhs`, and source the installed
`environment-setup-*` file. `arm-none-eabi-gcc` is for bare-metal code.

To change thread limits or add project settings without editing the baseline:

```yaml
# kas/project.local.yml
header:
  version: 14
local_conf_header:
  project: |
    BB_NUMBER_THREADS = "4"
    PARALLEL_MAKE = "-j4"
```

```sh
yocto-fhs . build kas/zynq.yml:kas/project.local.yml
```

kas regenerates `local.conf` and `bblayers.conf`; put lasting changes in YAML
or your project layer. Layer commits and Nix packages are pinned independently;
update them deliberately. Allow at least 90 GB of free disk space for an image
build; more is useful for multiple builds and caches. On NixOS, an FHS shell is
outside Yocto's tested host distributions. If host compiler/libc compatibility
causes trouble, install and source that Yocto release's `buildtools-extended`
inside the FHS shell, or use a supported Linux build host.

## Cora serial and JTAG

Connect the Cora's USB UART and use its stable device path:

```sh
embedded-fhs
ls /dev/serial/by-id/
picocom --baud 115200 --flow n /dev/serial/by-id/<your-cora-uart>
```

picocom exits with Ctrl-A, Ctrl-X. Serial and JTAG devices must be accessible to
your host user; this flake does not install udev rules or change group membership.
Vivado's Hardware Manager can use the board's JTAG interface through the
installed AMD cable support. OpenOCD is also available for projects with a
matching adapter and Zynq target configuration.

Built images appear under the Yocto build directory's `tmp/deploy/images/`.
The shell includes `ssh` and `rsync` for network deployment; flashing or booting
an image depends on the target board and its BSP.

## Vivado

Download the Linux installer from AMD and install Vivado under
`$HOME/tools/Xilinx` (the installation root, not the `Vivado` subdirectory).
This avoids needing root access. You can run the installer
from `embedded-fhs` if it needs FHS library paths. Then select the installed
version and launch the GUI:

```sh
vivado-fhs
```

Arguments are passed to Vivado, for example `vivado-fhs -mode batch -source
build.tcl`. The wrapper sources that version's `settings64.sh` before launch.
The flake defaults to `$HOME/tools/Xilinx/2025.2/Vivado`. Override
`VIVADO_DIR` in a local, ignored `.envrc.local` if you use another location.
The FHS environment supplies ncurses ABI 5, pixman, and libpng. The Vivado
wrapper explicitly searches `/usr/lib` so it can load the `libncurses.so.5`
compatibility alias. After changing the flake, exit any existing FHS shell
and run `direnv reload` before launching `vivado-fhs` again.
The wrapper also selects an isolated Fontconfig configuration with DejaVu
fonts, avoiding host font rules that Vivado's older Fontconfig cannot parse.

If Vitis was installed alongside Vivado, launch its IDE with:

```sh
vitis-fhs
```

The flake defaults to `$HOME/tools/Xilinx/2025.2/Vitis`; override `VITIS_DIR`
for a different installation. The AMD launcher starts the IDE in the
background; `vitis-fhs` captures its startup output in
`~/.local/state/vitis-fhs/launch.log` for troubleshooting.
The FHS environment includes `libudev`, which Electron loads dynamically
during GUI startup and which does not appear in its `ldd` dependency list,
and `libxkbfile` for the IDE's native keyboard module.
Vitis also writes backend logs to `~/.Xilinx/Vitis/2025.2/logs/ide.log`.

The installer may suggest running `scripts/installLibs.sh` as root. That script
installs distribution packages with `apt`, `dnf`, or `yum`; this flake supplies
the corresponding user-space libraries in the FHS shell instead. Run Vivado and
Vitis through the wrappers above. Kernel drivers and USB permissions, if
needed, still belong to the host system.

AMD supports specific Linux distributions and Vivado releases. NixOS with an
FHS shell is outside that support matrix. A license may be required for your
Vivado version and device. USB/JTAG access also depends on host permissions and
cable drivers; the FHS shell does not configure them.

## References

- [Yocto Scarthgap host requirements](https://docs.yoctoproject.org/scarthgap/ref-manual/system-requirements.html)
- [AMD 2025.2 layer requirements and generic-machine setup](https://github.com/Xilinx/meta-xilinx/blob/rel-v2025.2/meta-xilinx-core/README.md)
- [kas configuration and pinned revisions](https://kas.readthedocs.io/en/latest/userguide/project-configuration.html)
- [Cora Z7 reference material](https://digilent.com/reference/programmable-logic/cora-z7/start)
- [Digilent's historical Cora Z7-07S PetaLinux BSP (2017.4)](https://github.com/Digilent/Petalinux-Cora-Z7-07S)
