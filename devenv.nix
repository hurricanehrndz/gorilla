{ pkgs, ... }:
{
  # https://devenv.sh/languages/
  # rolling nixpkgs ships Go 1.26; go.mod requires 1.26.0. If the shell ever
  # provides an older Go, pin it with `languages.go.package = pkgs.go_1_26;`.
  languages.go.enable = true;

  # https://devenv.sh/packages/
  packages = with pkgs; [
    just
    golangci-lint
    nodejs_22
    pkg-config
    gtk4
    webkitgtk_6_0
  ];

  # https://devenv.sh/integrations/treefmt/
  treefmt = {
    enable = true;
    config.programs = {
      nixfmt.enable = true;
      gofumpt.enable = true;
      yamlfmt.enable = true;
    };
  };

  # https://devenv.sh/git-hooks/
  # Run treefmt on commit. Enabling the treefmt module above wires its
  # config-baked wrapper into this hook, so we only switch the hook on.
  git-hooks.hooks.treefmt.enable = true;

  # Lint changed Go files on commit. Pin the hook to the same golangci-lint
  # from `packages` above so the CLI and the hook never drift; config lives in
  # .golangci.yml (v2 schema). The stock entry full-lints every touched
  # package, which resurfaces the pre-existing findings deferred to the
  # kernel-repair workstream — lint only lines new since main instead, same
  # as `just lint main` and CI.
  git-hooks.hooks.golangci-lint = {
    enable = true;
    package = pkgs.golangci-lint;
    entry = "${pkgs.golangci-lint}/bin/golangci-lint run --new-from-merge-base=main ./...";
    pass_filenames = false;
  };
}
