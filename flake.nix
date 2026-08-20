{
  description = "Mock and proxy servers for the Telegram Bot API";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/release-26.05";

  outputs =
    { nixpkgs, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          telegym = import ./nix/package.nix {
            inherit pkgs systems;
            src = ./.;
          };
        in
        {
          inherit telegym;
          default = telegym;
        }
      );
    };
}
