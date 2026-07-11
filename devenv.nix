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
  ];
}
