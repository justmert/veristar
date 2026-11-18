// runs inside the browser container (tests/fixtures/browserbox). it gives
// the container the host's loopback ports the browsers use, then runs the
// playwright server the test runner on the host connects to.
import { spawn } from "node:child_process";
import { connect, createServer } from "node:net";

const ports = (process.env.BOX_PORTS ?? "").split(",").filter(Boolean);
for (const port of ports) {
  // half-open sockets on both sides, so an end of stream is passed on
  // instead of closing a connection that still has data to deliver
  createServer({ allowHalfOpen: true }, (c) => {
    const up = connect({ path: `/box/sock/${port}.sock`, allowHalfOpen: true });
    c.pipe(up);
    up.pipe(c);
    c.on("error", () => up.destroy());
    up.on("error", () => c.destroy());
  })
    .on("error", (err) => {
      console.error(`browserbox: relay on 127.0.0.1:${port}: ${err.message}`);
      process.exit(1);
    })
    .listen(Number(port), "127.0.0.1");
}

// --unsafe lets the launch options of the projects through
// (ignoreDefaultArgs, firefoxUserPrefs); the container is the test's own
const server = spawn(
  process.execPath,
  [
    "/pw/cli.js",
    "run-server",
    "--host",
    "0.0.0.0",
    "--port",
    process.env.BOX_LISTEN ?? "3000",
    "--unsafe",
  ],
  { stdio: "inherit" },
);
server.on("exit", (code) => process.exit(code ?? 1));
for (const sig of ["SIGTERM", "SIGINT"]) process.on(sig, () => server.kill(sig));
