{
  pkgs,
  lib,
  config,
  ...
}:
{
  # https://devenv.sh/packages/
  packages = [
    config.languages.python.package.pkgs.discordpy
    config.languages.python.package.pkgs.requests
  ];

  # https://devenv.sh/languages/
  languages = {
    python.enable = true;
    go.enable = true;
  };

  # See full reference at https://devenv.sh/reference/options/
}

