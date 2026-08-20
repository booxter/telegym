{
  description = "Mock and proxy servers for the Telegram Bot API";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/release-26.05";

  outputs =
    { self, nixpkgs, ... }:
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

      checks = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        nixpkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          nixos-service = import ./nix/tests/service.nix {
            inherit pkgs;
            module = self.nixosModules.default;
            package = self.packages.${system}.telegym;
          };
        }
      );

      nixosModules.default = import ./nix/module.nix { inherit self; };
    };
}
