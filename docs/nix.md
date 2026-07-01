# Nix support

This repository ships a Nix flake with:

- `packages.notes-server` — the default server binary
- `packages.upload-presentation` — the upload helper
- `apps.default` — runs `notes-server`
- `apps.upload-presentation` — runs the helper directly
- `devShells.default` — a small Go development shell
- `nixosModules.default` — a NixOS module for the server

## Build

Build the default server package:

```bash
nix build .#notes-server
```

The result is a symlink named `result` that points at the built package.

Build the upload helper:

```bash
nix build .#upload-presentation
```

## Run

Run the server directly from the flake:

```bash
nix run .
```

Run the upload helper:

```bash
nix run .#upload-presentation -- \
  --server-url=http://127.0.0.1:1947 \
  --html-file=./public/index.html
```

## Development shell

Enter a shell with Go and Make available:

```bash
nix develop
```

## Include in another flake

You can consume this project from another Nix flake via the exported
`nixosModules.default` module.

Example:

```nix
{
  description = "Mein NixOS Host";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

    remote-notes-server.url = "github:your-org/remote-notes-server";

    # Optional, but recommended:
    # keep this project on the same nixpkgs as the host.
    remote-notes-server.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = { self, nixpkgs, remote-notes-server, ... }:
    {
      nixosConfigurations.mein-host = nixpkgs.lib.nixosSystem {
        system = "x86_64-linux";

        modules = [
          ./configuration.nix

          remote-notes-server.nixosModules.default

          {
            services.remote-notes-server = {
              enable = true;
              hostname = "0.0.0.0";
              port = 1947;
              presentationDir = "/var/lib/remote-notes-server/presentation";
              presentationsDir = "/var/lib/remote-notes-server/presentations";
              accessToken = "super-secret-token";
              openFirewall = true;
            };
          }
        ];
      };
    };
}
```

The module is exposed as `nixosModules.default` and configures
`services.remote-notes-server`.

The service uses a writable state directory under
`/var/lib/remote-notes-server` by default.

## NixOS module options

Common options:

- `services.remote-notes-server.enable`
- `services.remote-notes-server.hostname`
- `services.remote-notes-server.port`
- `services.remote-notes-server.presentationDir`
- `services.remote-notes-server.presentationsDir`
- `services.remote-notes-server.presentationIndex`
- `services.remote-notes-server.presentationTtl`
- `services.remote-notes-server.activeTtlMs`
- `services.remote-notes-server.accessToken`
- `services.remote-notes-server.idleShutdownMs`
- `services.remote-notes-server.openFirewall`
- `services.remote-notes-server.extraFlags`
## Updating the vendor hash

`nix/package.nix` uses `buildGoModule`, so the Go module vendor hash must match
the current dependency set.

If dependencies change, temporarily set `vendorHash = lib.fakeHash;`, run a
Nix build, and replace it with the hash printed by Nix.
