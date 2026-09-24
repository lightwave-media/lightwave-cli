{
  description = "lightwave-cli — `lw` toolchain (dev + CI parity)";

  inputs = {
    # Same channel lightwave-core's flake uses, so the two repos resolve one
    # package set rather than drifting into two. Pinned by flake.lock.
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      systems = [ "aarch64-darwin" "x86_64-darwin" "aarch64-linux" "x86_64-linux" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      devShells = forAllSystems (pkgs:
        let
          # Scoped to what the `ci` task graph ACTUALLY invokes, parsed from
          # mise.toml rather than guessed: across fmt-check, vet, lint, build,
          # test, codespell, tidy, schema-drift, actions-pinned,
          # failure-digest, release-ship-destination and lw-current, the only
          # binaries are bash, codespell, go, golangci-lint and `lw` itself
          # (built from source by the tasks that need it). jq and git are added
          # because dev/hooks/* require them.
          #
          # Versions measured 2026-09-22 against the locked rev (flake.lock,
          # nixpkgs b6c98e9e6633):
          #
          #     tool            mise.toml   this shell   verdict
          #     go              1.26.7      1.26.7       EXACT — also go.mod's
          #     golangci-lint   v2.13.2     2.13.2       EXACT
          #     codespell       (unpinned)  2.4.3        see below
          #
          # go and golangci-lint agreeing EXACTLY is what makes this repo the
          # right place for Nix to own versions: golangci-lint is the one tool
          # whose version changes lint verdicts, and there is no gap to close.
          #
          # codespell is the interesting one. The `codespell` gate runs it, but
          # mise does NOT pin it — on this host it resolved to
          # /opt/homebrew/bin/codespell, i.e. the gate depended on whatever
          # Homebrew happened to have. Naming it here does not create a second
          # owner; it creates the FIRST one.
          common = with pkgs; [
            go
            golangci-lint
            codespell
            jq
            git
          ];
        in
        {
          # Daily work.
          default = pkgs.mkShell {
            packages = common;
            shellHook = ''
              echo "lightwave-cli devShell — $(go version | cut -d' ' -f3), golangci-lint $(golangci-lint version --short 2>/dev/null || echo '?'), codespell $(codespell --version)"
              echo "NOTE: mise still owns the task graph, pre-commit and goreleaser (see flake.nix)."
            '';
          };

          # What CI enters. Identical package set today — kept as a separate
          # output so CI can diverge (leaner, no shellHook noise) without
          # touching the shell humans use. Same shape as lightwave-core, whose
          # Forgejo gate runs `nix develop .#ci -c mise run ci`.
          ci = pkgs.mkShell {
            packages = common;
          };
        });

      # `lw` itself, built the way .goreleaser.yaml builds it (CGO off, the
      # same three ldflags), so a host can pin
      #
      #     github:lightwave-media/lightwave-cli/v3.15.0#lw
      #
      # and get the binary released under that tag. Nix builds are
      # revision-addressed — a flake ref loses its tag name — so the binary
      # reports git-<rev> and the tag→commit mapping stays where it lives, in
      # git; GoReleaser tarballs remain the version-named artifacts. There is
      # still ONE installer: `mise run lw:sync` builds this output for the
      # pinned ref and links it into ~/.local/bin. home.packages does not
      # list lw, so two copies never race in PATH.
      packages = forAllSystems (pkgs:
        let
          rev = self.shortRev or "dirty";
          versionPkg = "github.com/lightwave-media/lightwave-cli/internal/version";
          lw = pkgs.buildGoModule {
            pname = "lw";
            version = "0-git-${rev}";
            src = self;
            subPackages = [ "cmd/lw" ];
            vendorHash = "sha256-PMDbMAyi15qBW6b+ELNkrVgTAw0ygT/C/6j/9thX480=";
            env.CGO_ENABLED = 0;
            ldflags = [
              "-s"
              "-w"
              "-X ${versionPkg}.Version=git-${rev}"
              "-X ${versionPkg}.Commit=${rev}"
              "-X ${versionPkg}.Date=${self.lastModifiedDate or "unknown"}"
            ];
            # `mise run ci` is the gate and has already run the suite on the
            # commit being packaged; the package proves the build, not the tests.
            doCheck = false;
            meta.mainProgram = "lw";
          };
        in
        {
          inherit lw;
          default = lw;
        });

      formatter = forAllSystems (pkgs: pkgs.nixpkgs-fmt);
    };
}
