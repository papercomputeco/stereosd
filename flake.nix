{
  description = "stereosd - StereOS control plane daemon";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    dagger.url = "github:dagger/nix";
    dagger.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = { self, nixpkgs, flake-utils, dagger }:
    {
      overlays.default = final: prev: {
        stereosd = final.buildGoModule {
          pname = "stereosd";
          version = self.shortRev or "dev";
          src = final.lib.cleanSource self;
          vendorHash = null;
          ldflags = [
            "-s" "-w"
            "-X github.com/papercomputeco/stereosd/pkg/version.Version=${self.shortRev or "dev"}"
          ];
          env.CGO_ENABLED = 0;

          # Tests require system-level capabilities (vsock, mount, shutdown)
          # that are unavailable in the Nix build sandbox.
          doCheck = false;
        };
      };

      # NixOS module systemd service definition
      nixosModules.default = { config, lib, pkgs, ... }:
        let cfg = config.services.stereosd; in
        {
          options.services.stereosd = {
            enable = lib.mkEnableOption "stereosd daemon";
            package = lib.mkOption {
              type = lib.types.package;
              default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
              description = "The stereosd package to use";
            };
            listenMode = lib.mkOption {
              type = lib.types.enum [ "auto" "vsock" "tcp" ];
              default = "auto";
              description = ''
                Control plane listener mode.
                "vsock" - AF_VSOCK only (Linux/KVM with vhost-vsock).
                "tcp"   - TCP only (macOS/HVF, QEMU user-mode networking).
                "auto"  - try vsock first, fall back to TCP.
              '';
            };
            extraArgs = lib.mkOption {
              type = lib.types.listOf lib.types.str;
              default = [];
              description = "Additional command-line arguments passed after 'run'.";
            };
          };

          config = lib.mkIf cfg.enable {
            systemd.services.stereosd = {
              description = "StereOS Control Plane Daemon";
              wantedBy = [ "multi-user.target" ];
              after = [ "network.target" "systemd-tmpfiles-setup.service" ];
              serviceConfig = {
                ExecStart = "${cfg.package}/bin/stereosd run --listen-mode ${cfg.listenMode} ${lib.escapeShellArgs cfg.extraArgs}";
                Restart = "always";
                DynamicUser = true;
                StateDirectory = "stereosd";
              };
            };
          };
        };
    }
    //
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ self.overlays.default ];
        };
      in
      {
        packages = {
          default = pkgs.stereosd;
          stereosd = pkgs.stereosd;
        };

        devShells.default = pkgs.mkShell {
          buildInputs = [
            # Go toolchain
            pkgs.go_1_25
            pkgs.gotools
            pkgs.ginkgo

            # Build tools
            pkgs.gnumake

            # Test tools
            pkgs.hurl
            dagger.packages.${system}.dagger
          ];

          shellHook = ''
            echo "stereosd development environment"
            echo ""
            echo "Go version: $(go version)"
            echo ""
            echo "Available make targets:"
            make help
          '';
        };
      }
    );
}
