{
  description = "lightwave-cli — `lw` toolchain (dev + CI parity)";

  inputs = {
    # The fleet's one nixpkgs pin (nix/fleet.nix), so every repository's dev
    # shell and gate resolve from the same store.
    nixpkgs.url = "github:NixOS/nixpkgs/79b35bf0bda5cd110f856aa5b5b2c5ba4460dbf5";
  };

  outputs = { self, nixpkgs }:
    let
      systems = [ "aarch64-darwin" "x86_64-darwin" "aarch64-linux" "x86_64-linux" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      # A git hook exports GIT_DIR and friends pointing at the repo that ran
      # it; the steps below that shell out to git (golangci-lint's merge-base
      # ratchet, the tests' fixture repos) must not inherit them.
      unsetGit = "unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_OBJECT_DIRECTORY GIT_COMMON_DIR GIT_PREFIX GIT_CONFIG_PARAMETERS; ";
    in
    # The dev shell and the gate (`nix run .#ci`) come from the fleet's
    # template (nix/fleet.nix, stamped from nix-config); this repo declares
    # its tools and its gate's steps. Everything below is its own.
    import ./nix/fleet.nix {
      inherit nixpkgs;
      name = "lightwave-cli";
      # go 1.26.7 and golangci-lint 2.13.2, exactly mise.toml's pins.
      kinds = [ "go" ];
      # The rest of mise.toml's [tools], plus codespell (the gate runs it) and
      # a C compiler (`go test -race` needs cgo).
      tools = pkgs: with pkgs; [
        codespell
        goreleaser
        go-junit-report
        pre-commit
        nodejs_24
        python312
        stdenv.cc
      ];
      # mise.toml's [tasks.ci] graph, one task per step (or two where the task
      # runs two commands), in its `depends` order.
      gate = [
        # fmt-check
        "test -z \"$(gofmt -l .)\""
        # vet
        "go vet ./..."
        # lint: ratcheted to new code vs origin/main.
        "${unsetGit}golangci-lint run --new-from-merge-base=origin/main --timeout=10m ./..."
        # build
        "go build ./cmd/lw"
        # test
        "${unsetGit}go test -race -shuffle=on ./..."
        # codespell (reads .codespellrc)
        "codespell"
        # tidy: go mod tidy must be a no-op; go.mod/go.sum are restored.
        ''
          cp go.mod go.mod.cibak && cp go.sum go.sum.cibak
          go mod tidy
          status=0
          diff -q go.mod go.mod.cibak >/dev/null || { echo "go.mod not tidy — run 'go mod tidy' and commit"; status=1; }
          diff -q go.sum go.sum.cibak >/dev/null || { echo "go.sum not tidy — run 'go mod tidy' and commit"; status=1; }
          mv go.mod.cibak go.mod && mv go.sum.cibak go.sum
          exit $status
        ''
        # schema-drift: the CLI against lightwave-core's commands.yaml, read
        # from a sibling checkout ($LW_STAMP_ROOT, else $LW_LIGHTWAVE_ROOT,
        # else ~/dev). See mise.toml's [tasks.schema-drift] for why it fails
        # rather than skips when that checkout is missing.
        ''
          root="''${LW_STAMP_ROOT:-''${LW_LIGHTWAVE_ROOT:-$HOME/dev}}"
          stamp="$root/lightwave-core/src/schemas/interfaces/cli/commands.yaml"
          if [ ! -f "$stamp" ]; then
            echo "schema-drift: no lightwave-core stamp at $stamp"
            echo "  clone lightwave-core beside this repo, or set LW_STAMP_ROOT."
            exit 1
          fi
          go build -o ./bin/lw ./cmd/lw || exit 1
          LW_LIGHTWAVE_ROOT="$root" LW_SURFACE_GATE_STRICT=1 go test ./internal/cli/ -run TestCommandSurface || exit 1
          LW_LIGHTWAVE_ROOT="$root" LW_CHECK_SCHEMA_STRICT=1 ./bin/lw check schema || exit 1
        ''
        # actions-pinned: the proof, then the gate.
        "bash scripts/check-actions-pinned-test.sh"
        "bash scripts/check-actions-pinned.sh"
        # failure-digest
        "bash scripts/failure-digest-test.sh"
        # release-ship-destination
        "bash scripts/release-ship-destination-test.sh"
        # lw-current: the proof, then the (never-blocking) notice.
        "${unsetGit}bash scripts/lw-current-test.sh"
        "${unsetGit}bash scripts/lw-current.sh"
      ];
    } // {
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
            # `nix run .#ci` is the gate and has already run the suite on the
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
