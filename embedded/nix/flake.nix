{
  description = "FHS development environment for Vivado and Yocto";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { nixpkgs, ... }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      xilinxVersion = "2025.2";
      yoctoPython = pkgs.python312.withPackages (p: with p; [
        gitpython jinja2 pexpect pip subunit
      ]);
      # OE Scarthgap still checks for the historical lz4c executable.
      lz4Compat = pkgs.writeShellScriptBin "lz4c" ''
        exec ${pkgs.lz4}/bin/lz4 "$@"
      '';
      fontsConf = pkgs.makeFontsConf {
        fontDirectories = [ pkgs.dejavu_fonts ];
        # Keep rules compatible with AMD's bundled Fontconfig, while giving
        # Java/Electron predictable generic font families.
        includes = [ (pkgs.writeText "embedded-font-aliases.conf" ''
          <?xml version="1.0"?>
          <!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
          <fontconfig>
            <alias><family>sans-serif</family><prefer><family>DejaVu Sans</family></prefer></alias>
            <alias><family>serif</family><prefer><family>DejaVu Serif</family></prefer></alias>
            <alias><family>monospace</family><prefer><family>DejaVu Sans Mono</family></prefer></alias>
          </fontconfig>
        '') ];
      };

      fhs = pkgs.buildFHSEnv {
        name = "embedded-fhs";

        targetPkgs = p: with p; [
          # Yocto build host tools.
          bash
          bc
          binutils
          bison
          bzip2
          chrpath
          coreutils
          cpio
          curl
          diffutils
          diffstat
          file
          findutils
          flex
          gawk
          gcc13
          git
          gnugrep
          gnumake
          gnupatch
          gnused
          graphviz
          gzip
          iputils
          acl
          kas
          lz4
          lz4Compat
          nettools
          openssh
          perl
          pkg-config
          yoctoPython
          rpcsvc-proto
          rsync
          socat
          gnutar
          texinfo
          unzip
          util-linux
          wget
          which
          xz
          zip
          zstd

          # Zynq hardware bring-up and bare-metal development. Linux application
          # development should use the SDK built by the project's Yocto layers.
          cmake
          dtc
          gcc-arm-embedded
          gdb
          libusb1
          ninja
          openocd
          picocom
          ubootTools
          usbutils

          # Libraries used by the proprietary Vivado desktop binaries.
          alsa-lib
          at-spi2-atk
          at-spi2-core
          atk
          cairo
          cups
          dbus
          expat
          fontconfig
          freetype
          gdk-pixbuf
          glib
          gtk3
          libGL
          libdrm
          libgbm
          libpng
          libsecret
          libxcrypt-legacy
          libxkbcommon
          ncurses
          ncurses5
          nspr
          nss
          openssl
          pango
          pixman
          stdenv.cc.cc.lib
          # Electron loads libudev at runtime, so ldd does not report it.
          udev
          zlib
          libice
          libsm
          libx11
          libxcomposite
          libxcursor
          libxdamage
          libxext
          libxfixes
          libxi
          libxkbfile
          libxinerama
          libxrandr
          libxrender
          libxscrnsaver
          libxt
          libxtst
          libxcb
          xorg-server
        ];

        multiPkgs = p: with p; [
          zlib
          ncurses
          ncurses5
          stdenv.cc.cc.lib
        ];

        runScript = "bash";
        profile = ''
          export LANG=en_US.UTF-8
          export LC_ALL=en_US.UTF-8
          export EMBEDDED_FHS=1
          export SHELL=/bin/bash
          # mkShell's compiler/linker settings must not leak into BitBake.
          unset NIX_CFLAGS_COMPILE NIX_LDFLAGS NIX_CFLAGS_LINK
          unset CC CXX CPP LD AR AS NM STRIP RANLIB OBJCOPY OBJDUMP
        '';
      };

      yocto = pkgs.writeShellScriptBin "yocto-fhs" ''
        set -eu
        exec ${fhs}/bin/embedded-fhs ${./scripts/yocto} "$@"
      '';

      vivado = pkgs.writeShellScriptBin "vivado-fhs" ''
        set -eu
        VIVADO_DIR="''${VIVADO_DIR:-$HOME/tools/Xilinx/${xilinxVersion}/Vivado}"
        if [ ! -f "$VIVADO_DIR/settings64.sh" ]; then
          echo "Missing $VIVADO_DIR/settings64.sh; set VIVADO_DIR to your installation path." >&2
          exit 2
        fi
        exec ${fhs}/bin/embedded-fhs -c '
          source "$1/settings64.sh"
          # Vivado bundles an older Fontconfig that cannot parse host rules.
          export FONTCONFIG_FILE=${fontsConf}
          # ldconfig indexes ncurses5 by its wide-character SONAME, so the
          # libncurses.so.5 compatibility alias needs an explicit search path.
          export LD_LIBRARY_PATH="/usr/lib''${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
          shift
          exec vivado "$@"
        ' bash "$VIVADO_DIR" "$@"
      '';

      vitis = pkgs.writeShellScriptBin "vitis-fhs" ''
        set -eu
        VITIS_DIR="''${VITIS_DIR:-$HOME/tools/Xilinx/${xilinxVersion}/Vitis}"
        if [ ! -f "$VITIS_DIR/settings64.sh" ]; then
          echo "Missing $VITIS_DIR/settings64.sh; set VITIS_DIR to your installation path." >&2
          exit 2
        fi
        exec ${fhs}/bin/embedded-fhs -c '
          source "$1/settings64.sh"
          export FONTCONFIG_FILE=${fontsConf}
          ide="$1/ide/electron-app/lnx64/vitis-ide"
          missing="$(ldd "$ide" | grep "not found" || true)"
          if [ -n "$missing" ]; then
            printf "Vitis IDE is missing libraries:\n%s\n" "$missing" >&2
            exit 1
          fi
          log_dir="''${XDG_STATE_HOME:-$HOME/.local/state}/vitis-fhs"
          mkdir -p "$log_dir"
          umask 077
          export VITIS_FHS_LOG="$log_dir/launch.log"
          : > "$VITIS_FHS_LOG"
          # AMD starts Electron in the background and redirects its output to
          # /dev/null. The exported Bash function captures that output instead.
          setsid() {
            "$@" --enable-logging=stderr --disable-gpu --ozone-platform=x11 > "$VITIS_FHS_LOG" 2>&1
            result=$?
            printf "\nVitis IDE exited with status %s\n" "$result" >> "$VITIS_FHS_LOG"
            return "$result"
          }
          export -f setsid
          echo "Vitis IDE launch log: $VITIS_FHS_LOG" >&2
          shift
          # Source the AMD launcher with its own path as $0. Its normal
          # script exits as soon as it backgrounds Electron; waiting keeps
          # the FHS mount namespace alive for the IDE process.
          exec bash -c "trap wait EXIT; source \"\$0\" \"\$@\"" "$VITIS_DIR/bin/vitis" "$@"
        ' bash "$VITIS_DIR" "$@"
      '';
    in
    {
      packages.${system} = {
        embedded-fhs = fhs;
        vivado-fhs = vivado;
        vitis-fhs = vitis;
        yocto-fhs = yocto;
        default = fhs;
      };

      devShells.${system}.default = pkgs.mkShell {
        packages = [ fhs vivado vitis yocto ];
        shellHook = ''
          export VIVADO_DIR="''${VIVADO_DIR:-$HOME/tools/Xilinx/${xilinxVersion}/Vivado}"
          export VITIS_DIR="''${VITIS_DIR:-$HOME/tools/Xilinx/${xilinxVersion}/Vitis}"
          echo "Embedded tools: embedded-fhs, yocto-fhs, vivado-fhs, vitis-fhs"
        '';
      };
    };
}
