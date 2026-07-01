{ lib
, buildGoModule
, pname
, version
, subPackage
, vendorHash ? lib.fakeHash
, ...
}:

buildGoModule {
  inherit pname version vendorHash;

  src = builtins.path {
    name = "remote-notes-server-source";
    path = ../.;
    filter = path: type:
      let
        rel = lib.removePrefix "${toString ../.}/" (toString path);
      in
        !(lib.hasPrefix ".git/" rel
          || lib.hasPrefix ".pi/" rel
          || lib.hasPrefix ".cache/" rel
          || lib.hasPrefix "dist/" rel
          || lib.hasPrefix "node_modules/" rel
          || lib.hasPrefix "notes-server" rel
          || lib.hasPrefix "upload-presentation" rel
          || rel == "result");
  };
  subPackages = [ subPackage ];

  ldflags = [ "-s" "-w" ];

  meta = {
    description = if pname == "notes-server"
      then "Reveal.js speaker notes server"
      else "Presentation upload helper for Reveal.js speaker notes server";
    license = lib.licenses.mit;
    mainProgram = pname;
  };
}
