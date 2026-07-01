{
  description = "Remote Notes Server";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
  };

  outputs = { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];

      forAllSystems = nixpkgs.lib.genAttrs systems;

      pkgsFor = system:
        import nixpkgs {
          inherit system;
        };

      version = "0.1.0";
      vendorHash = "sha256-Nd6VVnT0wf0+65uEIdPd5ytei2/wIdj4nGSHjprPcn4=";
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = pkgsFor system;
        in
        {
          notes-server = pkgs.callPackage ./nix/package.nix {
            pname = "notes-server";
            inherit version vendorHash;
            subPackage = "cmd/notes-server";
          };

          upload-presentation = pkgs.callPackage ./nix/package.nix {
            pname = "upload-presentation";
            inherit version vendorHash;
            subPackage = "cmd/upload-presentation";
          };

          default = self.packages.${system}.notes-server;
        });

      apps = forAllSystems (system:
        {
          default = {
            type = "app";
            program = "${self.packages.${system}.default}/bin/notes-server";
          };

          upload-presentation = {
            type = "app";
            program = "${self.packages.${system}.upload-presentation}/bin/upload-presentation";
          };
        });

      devShells = forAllSystems (system:
        let
          pkgs = pkgsFor system;
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gnumake
            ];
          };
        });

      nixosModules.default = import ./nix/module.nix {
        inherit self;
      };
    };
}
