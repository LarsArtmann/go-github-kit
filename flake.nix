{
  description = "go-github-kit — operational kernel over google/go-github: auth, rate limiting, retry, ETag cache";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    systems.url = "github:nix-systems/default";

    flake-parts = {
      url = "github:hercules-ci/flake-parts";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };

    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    inputs@{ self, flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = import inputs.systems;

      imports = [ inputs.treefmt-nix.flakeModule ];

      perSystem =
        {
          config,
          pkgs,
          lib,
          ...
        }:
        let
          goPkg = pkgs.go_1_26;
        in
        {
          devShells.default = pkgs.mkShellNoCC {
            packages = builtins.attrValues {
              inherit (pkgs)
                go_1_26
                golangci-lint
                govulncheck
                golines
                nixfmt
                actionlint
                ;
            };

            GOTOOLCHAIN = "local";
            GOEXPERIMENT = "jsonv2";
          };

          packages.default = pkgs.buildGoModule {
            pname = "go-github-kit";
            version = self.rev or self.dirtyRev or "dev";
            src = ./.;
            vendorHash = "sha256-wqtcZ+Nz5S1Qm4hlAufoxgklwkUnc2vWJWvSxATYRA0=";

            meta = with lib; {
              description = "Operational kernel over google/go-github: auth, rate limiting, retry, ETag cache";
              homepage = "https://github.com/LarsArtmann/go-github-kit";
              license = licenses.mit;
              platforms = platforms.unix;
            };

            env.CGO_ENABLED = "0";
            env.GOWORK = "off";
            doCheck = false;
          };

          apps = {
            test = {
              type = "app";
              meta.description = "Run the Go test suite with the race detector";
              program = "${
                pkgs.writeShellApplication {
                  name = "test";
                  runtimeInputs = [ goPkg ];
                  text = ''
                    export CGO_ENABLED=0 GOTOOLCHAIN=local GOEXPERIMENT=jsonv2
                    exec go test -race "$@" ./...
                  '';
                }
              }/bin/test";
            };

            lint = {
              type = "app";
              meta.description = "Run golangci-lint";
              program = "${
                pkgs.writeShellApplication {
                  name = "lint";
                  runtimeInputs = [
                    goPkg
                    pkgs.golangci-lint
                  ];
                  text = ''
                    export GOTOOLCHAIN=local GOEXPERIMENT=jsonv2
                    exec golangci-lint run ./...
                  '';
                }
              }/bin/lint";
            };
          };

          treefmt = {
            programs = {
              nixfmt.enable = true;
              gofmt.enable = true;
            };
          };

          checks.build = config.packages.default;
          checks.format = config.treefmt.build.check self;
        };
    };
}
