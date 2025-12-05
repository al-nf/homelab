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
    pkgs.wakeonlan
  ];

  # https://devenv.sh/languages/
  languages = {
    python.enable = true;
    go.enable = true;
  };

  # https://devenv.sh/processes/
  processes = {
    discord-bot = {
      cwd = "projects/desk-cube/api/discord";
      exec = "python bot.py";
    };
    
    desk-cube-hub = {
      cwd = "projects/desk-cube/api/hub";
      exec = "([ -x ./hub ] || go build) && ./hub";
    };
    
    wake-on-lan = {
      cwd = "services/wake-on-lan";
      exec = "([ -x ./wolserver ] || go build) && ./wolserver";
    };
  };

  # See full reference at https://devenv.sh/reference/options/
}

