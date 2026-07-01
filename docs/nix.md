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

## NixOS module

The module is exposed as `nixosModules.default` and configures the service
`services.remote-notes-server`.

Example:

```nix
{
  inputs.remote-notes-server.url = "github:your-org/remote-notes-server";

  outputs = { self, nixpkgs, remote-notes-server, ... }:
    {
      nixosConfigurations.my-server = nixpkgs.lib.nixosSystem {
        system = "x86_64-linux";
        modules = [
          remote-notes-server.nixosModules.default
          ({ ... }: {
            services.remote-notes-server = {
              enable = true;
              hostname = "0.0.0.0";
              port = 1947;
              presentationDir = "/srv/remote-notes/presentation";
              presentationsDir = "/var/lib/remote-notes-server/presentations";
              accessToken = "super-secret-token";
              openFirewall = true;
            };
          })
        ];
      };
    };
}
```

The module uses a service-owned writable state directory for uploaded
presentations and metadata. By default, that state lives under
`/var/lib/remote-notes-server`.

## Updating the vendor hash

`nix/package.nix` uses `buildGoModule`, so the Go module vendor hash must match
the current dependency set.

If dependencies change, temporarily set `vendorHash = lib.fakeHash;`, run a
Nix build, and replace it with the hash printed by Nix.
