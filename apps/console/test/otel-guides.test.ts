import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { envTable, goRequires, otelGuides, otlpEndpoint, region } from "@/lib/otel-guides";

const examples = resolve(import.meta.dirname, "../../../examples");

function read(dir: string, path: string): string {
  return readFileSync(resolve(examples, "..", dir, path), "utf8");
}

describe("api analytics guides built from the example apps", () => {
  const guides = otelGuides();

  it("has the five languages of the example apps", () => {
    expect(guides.map((g) => g.id)).toEqual(["node", "go", "python", "rust", "java"]);
  });

  // a shown snippet must be the example's own code: every non-empty line,
  // in order, with only the common indentation of the region removed
  it("shows only lines that exist, in order, in the example files", () => {
    for (const g of guides) {
      for (const f of g.files) {
        const source = read(g.dir, f.path).split("\n");
        let at = 0;
        for (const line of f.code.split("\n")) {
          if (line.trim() === "") continue;
          const found = source.findIndex((s, i) => i >= at && s.endsWith(line) && s.trim() !== "");
          expect(found, `${g.dir}/${f.path}: ${line}`).toBeGreaterThanOrEqual(at);
          at = found + 1;
        }
        expect(f.code).not.toMatch(/docs:(start|end)/);
      }
    }
  });

  it("puts the bearer token only on the platform exporters", () => {
    const code = (id: string) =>
      guides
        .find((g) => g.id === id)
        ?.files.map((f) => f.code)
        .join("\n") ?? "";
    // biome-ignore lint/suspicious/noTemplateCurlyInString: the guide's source text must hold the placeholder itself
    expect(code("node")).toContain("Authorization: `Bearer ${config.platformToken}`");
    expect(code("node")).toContain(
      // biome-ignore lint/suspicious/noTemplateCurlyInString: the guide's source text must hold the placeholder itself
      "url: `${config.platformEndpoint}/v1/traces`,\n        headers: auth,",
    );
    expect(code("go")).toContain(
      'auth := map[string]string{"Authorization": "Bearer " + c.platformToken}',
    );
    expect(code("go")).toContain("otlpmetrichttp.WithHeaders(auth)");
    expect(code("python")).toContain('AUTH = {"Authorization": f"Bearer {PLATFORM_TOKEN}"}');
    expect(code("python")).toContain("metric_reader(PLATFORM_ENDPOINT, AUTH)");
    expect(code("rust")).toContain('format!("Bearer {}", c.platform_token)');
    expect(code("rust")).toContain(".with_headers(auth.clone())");
    // biome-ignore lint/suspicious/noTemplateCurlyInString: the guide's source text must hold the placeholder itself
    expect(code("java")).toContain("endpoint: ${PLATFORM_OTLP_ENDPOINT}/v1/metrics");
    expect(code("java").match(/value: Bearer \$\{PLATFORM_OTLP_TOKEN\}/g)).toHaveLength(2);
  });

  it("shows python's semantic conventions opt-in from the Dockerfile and the readme", () => {
    const py = guides.find((g) => g.id === "python");
    const docker = py?.files.find((f) => f.path === "Dockerfile");
    expect(docker?.code).toBe("ENV OTEL_SEMCONV_STABILITY_OPT_IN=http");
    const prose = py?.prose.map((b) => b.text).join("\n") ?? "";
    expect(prose).toContain("Set `OTEL_SEMCONV_STABILITY_OPT_IN=http`");
    expect(prose).toContain("docker run -e OTEL_SEMCONV_STABILITY_OPT_IN= ...");
  });

  it("shows the whole node telemetry file and package.json", () => {
    const node = guides.find((g) => g.id === "node");
    expect(node?.files.find((f) => f.path === "src/telemetry.js")?.code).toBe(
      read("examples/otel-node-express", "src/telemetry.js").trimEnd(),
    );
    expect(node?.files.find((f) => f.path === "package.json")?.code).toBe(
      read("examples/otel-node-express", "package.json").trimEnd(),
    );
  });

  it("lists the environment of every example from its readme", () => {
    for (const g of guides) {
      const names = g.env.map((r) => r.name);
      expect(names, g.id).toEqual(
        expect.arrayContaining([
          "TENANT_OTLP_ENDPOINT",
          "PLATFORM_OTLP_ENDPOINT",
          "PLATFORM_OTLP_TOKEN",
          "EXPORT_SIGNALS",
          "METRICS_HISTOGRAM",
        ]),
      );
      expect(g.envNote, g.id).toContain("the platform copy carries the bearer token");
    }
  });

  it("contains no em dash in any text it shows", () => {
    const all = JSON.stringify(guides);
    expect(all).not.toContain(String.fromCharCode(0x2014));
  });
});

describe("region", () => {
  const src = [
    "a",
    "  // docs:start one (a note)",
    "    x := 1",
    "",
    "      y := 2",
    "  // docs:end one",
    "# docs:start two",
    "# docs:end two",
  ].join("\n");

  it("returns the lines between the markers without the common indentation", () => {
    expect(region(src, "one")).toBe("x := 1\n\n  y := 2");
  });

  it("refuses a missing or empty region instead of showing less", () => {
    expect(() => region(src, "three")).toThrow('docs region "three" not found');
    expect(() => region(src, "two")).toThrow('docs region "two" not found');
  });
});

describe("goRequires", () => {
  it("returns the direct requirements only", () => {
    const mod = "module m\n\nrequire (\n\ta v1\n)\n\nrequire (\n\tb v2 // indirect\n)\n";
    expect(goRequires(mod)).toBe("require (\n\ta v1\n)");
  });
});

describe("envTable", () => {
  it("refuses a readme whose table changed shape", () => {
    expect(() => envTable("## Environment\n\n| Name | Purpose |\n|---|---|\n")).toThrow(
      "unexpected environment table header",
    );
  });
});

describe("otlpEndpoint", () => {
  it("derives otlp.<domain> from app.<domain>", () => {
    expect(otlpEndpoint("app.sa-test.example")).toBe("https://otlp.sa-test.example");
  });

  it("derives nothing from another host name", () => {
    expect(otlpEndpoint("localhost")).toBeNull();
    expect(otlpEndpoint("console.example.org")).toBeNull();
  });
});
