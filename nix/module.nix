{ self }:

{ config, lib, pkgs, ... }:

let
  cfg = config.services.remote-notes-server;

  defaultPackage = self.packages.${pkgs.stdenv.hostPlatform.system}.notes-server;

  serviceStateDir = "remote-notes-server";
  serviceRoot = "/var/lib/${serviceStateDir}";
  defaultPresentationsDir = "${serviceRoot}/presentations";
  defaultPresentationDir = "${serviceRoot}/presentation";

  args =
    [
      "--hostname" cfg.hostname
      "--port" (toString cfg.port)
      "--presentation-dir" cfg.presentationDir
      "--presentation-index" cfg.presentationIndex
      "--active-ttl-ms" (toString cfg.activeTtlMs)
      "--presentations-dir" cfg.presentationsDir
      "--presentation-ttl" cfg.presentationTtl
      "--access-token" cfg.accessToken
      "--idle-shutdown-ms" (toString cfg.idleShutdownMs)
    ]
    ++ cfg.extraFlags;
in
{
  options.services.remote-notes-server = {
    enable = lib.mkEnableOption "remote-notes-server";

    package = lib.mkOption {
      type = lib.types.package;
      default = defaultPackage;
      description = "Package that provides the remote-notes-server binary.";
    };

    hostname = lib.mkOption {
      type = lib.types.str;
      default = "0.0.0.0";
      description = "Hostname or bind address for the server.";
    };

    port = lib.mkOption {
      type = lib.types.port;
      default = 1947;
      description = "TCP port used by the notes server.";
    };

    presentationDir = lib.mkOption {
      type = lib.types.str;
      default = defaultPresentationDir;
      description = "Directory with the reveal.js presentation to serve at /.";
    };

    presentationIndex = lib.mkOption {
      type = lib.types.str;
      default = "/index.html";
      description = "Presentation entry file served from presentationDir.";
    };

    activeTtlMs = lib.mkOption {
      type = lib.types.ints.positive;
      default = 7200000;
      description = "Session TTL in milliseconds.";
    };

    presentationsDir = lib.mkOption {
      type = lib.types.str;
      default = defaultPresentationsDir;
      description = "Writable directory for uploaded presentations and metadata.";
    };

    presentationTtl = lib.mkOption {
      type = lib.types.str;
      default = "never";
      description = "TTL for uploaded presentations (never disables pruning; e.g. 24h, 7d, 4h30m).";
    };

    accessToken = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "Optional access token for protected API and browser sessions.";
    };

    idleShutdownMs = lib.mkOption {
      type = lib.types.ints.unsigned;
      default = 0;
      description = "Shut the server down after all clients disconnect for this long, in milliseconds.";
    };

    extraFlags = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Additional command-line flags appended to ExecStart.";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Open the service port in the NixOS firewall.";
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.remote-notes-server = {
      description = "Remote Notes Server";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];

      serviceConfig = {
        ExecStart = "${lib.getExe cfg.package} ${lib.escapeShellArgs args}";
        Restart = "on-failure";
        DynamicUser = true;
        StateDirectory = serviceStateDir;
        WorkingDirectory = serviceRoot;
      };
    };

    networking.firewall.allowedTCPPorts = lib.optional cfg.openFirewall cfg.port;
  };
}
