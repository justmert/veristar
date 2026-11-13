// test fixture only. creates a throwaway self-signed certificate for the fake
// hostnames so every page and the gateway are served over https. https matters:
// webcrypto (the proof) and fetch metadata headers exist only in secure contexts.
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export interface FixtureCert {
  key: Buffer;
  cert: Buffer;
}

export function createCert(hosts: string[]): FixtureCert {
  const dir = mkdtempSync(join(tmpdir(), "sa-e2e-cert-"));
  try {
    const keyPath = join(dir, "key.pem");
    const certPath = join(dir, "cert.pem");
    const san = hosts.map((h) => `DNS:${h}`).join(",");
    execFileSync(
      "openssl",
      [
        "req",
        "-x509",
        "-newkey",
        "ec",
        "-pkeyopt",
        "ec_paramgen_curve:prime256v1",
        "-nodes",
        "-days",
        "2",
        "-subj",
        "/CN=veristar e2e fixture",
        "-addext",
        `subjectAltName=${san}`,
        "-keyout",
        keyPath,
        "-out",
        certPath,
      ],
      { stdio: ["ignore", "ignore", "pipe"] },
    );
    return { key: readFileSync(keyPath), cert: readFileSync(certPath) };
  } finally {
    // the key only needs to live in memory for the lifetime of the fixture
    rmSync(dir, { recursive: true, force: true });
  }
}
