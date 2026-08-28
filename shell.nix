{ pkgs ? import <nixpkgs> {} }:

pkgs.mkShell {
  # Tools available in the environment
  buildInputs = with pkgs; [
    go              # Alternatively use specific versions like pkgs.go_1_22
    gopls
    gotools
    golangci-lint
  ];

  # Environment variables to set when entering the shell
  shellHook = ''
    export GOPATH="$HOME/go"
    export PATH="$GOPATH/bin:$PATH"
    export NIX_PROJECT="pib"
    go version
  '';
}
