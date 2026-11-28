// Discovery stub for the sandboxed live tiers (docs/design/private-binding.md
// §S2.10). unifi-core asks a host-side discovery agent for the host's network
// interfaces; on a real install the installer's own agent answers. In a
// container with no such agent it never finds a LAN IP and never writes its
// nginx site configs. This answers every request on 127.0.0.1:11002 with
// os.networkInterfaces(), the shape unifi-core reads. Our code, run with the
// image's own node; only started when E2E_SANDBOX is on.
const http = require("http");
const os = require("os");
http
  .createServer((req, res) => {
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify(os.networkInterfaces()));
  })
  .listen(11002, "127.0.0.1");
