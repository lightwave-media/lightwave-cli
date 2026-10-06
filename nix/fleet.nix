# The fleet's per-repository Nix, the same file in every repository of the
# forge as nix/fleet.nix. It is stamped by `forge-workflows` from
# kiwi-dev-la/nix-config (forgejo/fleet.nix), beside the CI workflow; change it
# there, not here: a copy that differs fails `forge-status`.
#
# A repository's flake.nix names its kinds and adds only what is its own:
#
#   inputs.nixpkgs.url = "github:NixOS/nixpkgs/79b35bf0bda5cd110f856aa5b5b2c5ba4460dbf5";
#   outputs = { self, nixpkgs }: import ./nix/fleet.nix {
#     inherit nixpkgs;
#     name = "my-repo";
#     kinds = [ "go" "node" ];                     # the templates below
#     tools = pkgs: [ pkgs.goreleaser ];           # beyond what the kinds bring
#     gate = [ "go vet ./..." "go test ./..." ];   # replaces the kinds' gate
#   };
#
# and gets:
#   devShells.default  the kinds' tools, the repository's own, and what every
#                      gate uses (bash, coreutils, git, jq ...): `nix develop`,
#                      and the shell every factory worker in a clone enters
#   apps.ci            the gate: each step in order, from the repository's
#                      root, with the same tools and nothing else; `nix run
#                      .#ci` is what the forge's runner runs (forge-ci).
#                      Without `gate`, the kinds' own steps, in kind order.
#
# Every repository pins the same nixpkgs (above) so one store serves them all.
# Other outputs (packages, checks, modules) are the repository's own:
# `import ./nix/fleet.nix { ... } // { packages = ...; }`.
{ nixpkgs
, name
, kinds ? [ ]
, tools ? (pkgs: [ ])
, gate ? null
, systems ? [ "aarch64-darwin" "x86_64-darwin" "aarch64-linux" "x86_64-linux" ]
}:
let
  inherit (nixpkgs) lib;
  forAllSystems = f: lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

  # One template per kind of repository: its tools, and the gate a
  # repository of that kind gets when it declares none.
  templates = {
    zig = {
      tools = pkgs: [ pkgs.zig pkgs.zls ];
      gate = [ "zig build test --summary all" ];
    };
    go = {
      tools = pkgs: [ pkgs.go pkgs.gopls pkgs.golangci-lint ];
      gate = [ "test -z \"$(gofmt -l .)\"" "go vet ./..." "go test ./..." ];
    };
    node = {
      tools = pkgs: [ pkgs.nodejs_24 ];
      gate = [ "npm ci" "npm test" ];
    };
    pnpm = {
      tools = pkgs: [ pkgs.nodejs_24 pkgs.pnpm ];
      gate = [ "pnpm install --frozen-lockfile" "pnpm test" ];
    };
    bun = {
      tools = pkgs: [ pkgs.bun ];
      gate = [ "bun install --frozen-lockfile" "bun test" ];
    };
    hugo = {
      tools = pkgs: [ pkgs.hugo ];
      gate = [ "hugo --gc --minify" ];
    };
    python = {
      tools = pkgs: [ pkgs.python312 ];
      gate = [ "python3 -m unittest discover" ];
    };
    terragrunt = {
      tools = pkgs: [ pkgs.opentofu pkgs.terragrunt ];
      gate = [ "tofu fmt -check -recursive" "terragrunt hcl format --check" ];
    };
    shell = {
      tools = pkgs: [ pkgs.shellcheck pkgs.actionlint ];
      gate = [ "git ls-files -z '*.sh' | xargs -0 -r shellcheck" ];
    };
  };

  unknown = lib.subtractLists (lib.attrNames templates) kinds;
  chosen = assert lib.assertMsg (unknown == [ ])
    "nix/fleet.nix: no template for kind(s) ${lib.concatStringsSep ", " unknown}; known: ${lib.concatStringsSep ", " (lib.attrNames templates)}";
    map (k: templates.${k}) kinds;

  # What the gate's own steps and the fleet's scripts call, on every host.
  base = pkgs: with pkgs; [ bash coreutils findutils gnugrep gnused gawk git jq ];
  everything = pkgs: lib.unique (base pkgs ++ lib.concatMap (t: t.tools pkgs) chosen ++ tools pkgs);
  steps = if gate != null then gate else lib.concatMap (t: t.gate) chosen;
in
assert lib.assertMsg (steps != [ ]) "nix/fleet.nix: ${name} has no gate: name a kind or list the steps";
{
  devShells = forAllSystems (pkgs: {
    default = pkgs.mkShell { packages = everything pkgs; };
  });

  apps = forAllSystems (pkgs: {
    ci = {
      type = "app";
      program = lib.getExe (pkgs.writeShellApplication {
        name = "${name}-ci";
        runtimeInputs = everything pkgs;
        # Each step is quoted whole on purpose: it expands when it runs.
        excludeShellChecks = [ "SC2016" ];
        text = ''
          cd "$(git rev-parse --show-toplevel)"
          step() { echo "▶ $1"; bash -c "$1"; }
        '' + lib.concatMapStrings (s: "step ${lib.escapeShellArg s}\n") steps;
      });
    };
  });
}
