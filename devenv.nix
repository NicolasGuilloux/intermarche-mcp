{ pkgs, lib, config, inputs, ... }:

{
  packages = [
    pkgs.git
  ];

  languages.go.enable = true;

  enterShell = ''
    echo "intermarche-mcp dev environment"
    go version
  '';

  scripts.build.exec = ''
    cd src && go build -o ../intermarche-mcp .
  '';

  enterTest = ''
    echo "Running tests"
    cd src && go test ./...
  '';

  dotenv.enable = true;
}
