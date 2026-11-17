// per-language setup instructions for api analytics, built from the example
// apps in examples/otel-*. vite includes the files as text at build time, so
// what a tenant reads is the code the example app tests run (tests/otelapps),
// never a retyped copy. source files mark the parts shown here with a
// "docs:start <name>" and "docs:end <name>" comment pair; readmes give the
// prose and the environment table.

import goMod from "../../../../examples/otel-go-nethttp/go.mod?raw";
import goMain from "../../../../examples/otel-go-nethttp/main.go?raw";
import goReadme from "../../../../examples/otel-go-nethttp/README.md?raw";
import javaDockerfile from "../../../../examples/otel-java-spring/Dockerfile?raw";
import javaConfig from "../../../../examples/otel-java-spring/otel.yaml?raw";
import javaReadme from "../../../../examples/otel-java-spring/README.md?raw";
import javaMain from "../../../../examples/otel-java-spring/src/main/java/example/Application.java?raw";
import javaStart from "../../../../examples/otel-java-spring/start.sh?raw";
import nodePackage from "../../../../examples/otel-node-express/package.json?raw";
import nodeReadme from "../../../../examples/otel-node-express/README.md?raw";
import nodeTelemetry from "../../../../examples/otel-node-express/src/telemetry.js?raw";
import pyApp from "../../../../examples/otel-python-fastapi/app.py?raw";
import pyDockerfile from "../../../../examples/otel-python-fastapi/Dockerfile?raw";
import pyProject from "../../../../examples/otel-python-fastapi/pyproject.toml?raw";
import pyReadme from "../../../../examples/otel-python-fastapi/README.md?raw";
import rustCargo from "../../../../examples/otel-rust-actix/Cargo.toml?raw";
import rustReadme from "../../../../examples/otel-rust-actix/README.md?raw";
import rustMain from "../../../../examples/otel-rust-actix/src/main.rs?raw";

export type GuideFile = {
  // path inside the example directory, shown as the file name
  path: string;
  caption: string;
  code: string;
};

export type EnvRow = { name: string; values: string; purpose: string };

export type ProseBlock = { kind: "p"; text: string } | { kind: "code"; text: string };

export type OtelGuide = {
  id: "node" | "go" | "python" | "rust" | "java";
  label: string;
  // the example's directory under examples/ in the repository
  dir: string;
  prose: ProseBlock[];
  files: GuideFile[];
  env: EnvRow[];
  envNote: string;
};

// lines between "docs:start name" and "docs:end name", without the marker
// lines and with the common indentation removed. a missing or empty region
// throws: the guide would otherwise show less than the example runs, and
// the console tests fail on it before a build ships.
export function region(source: string, name: string): string {
  const lines = source.split(/\r?\n/);
  const marker = (kind: "start" | "end") =>
    lines.findIndex((l) => new RegExp(`(//|#)\\s*docs:${kind} ${name}(\\s|$)`).test(l));
  const start = marker("start");
  const end = marker("end");
  if (start < 0 || end < 0 || end <= start + 1) {
    throw new Error(`docs region "${name}" not found`);
  }
  const body = lines.slice(start + 1, end);
  while (body.length && body[body.length - 1]?.trim() === "") body.pop();
  const indent = Math.min(
    ...body.filter((l) => l.trim() !== "").map((l) => /^[\t ]*/.exec(l)?.[0].length ?? 0),
  );
  const text = body.map((l) => l.slice(indent)).join("\n");
  if (text.trim() === "") throw new Error(`docs region "${name}" is empty`);
  return text;
}

// the first require block of a go.mod: the direct dependencies. the second
// block lists indirect ones, which go mod tidy maintains on its own.
export function goRequires(mod: string): string {
  const m = /^require \([\s\S]*?^\)$/m.exec(mod);
  if (!m) throw new Error("go.mod has no require block");
  return m[0];
}

// a readme split into its sections: the text before the first "## " is the
// introduction, keyed by the empty string
function sections(md: string): Map<string, string[]> {
  const out = new Map<string, string[]>([["", []]]);
  let current = "";
  for (const line of md.split(/\r?\n/)) {
    const h = /^## (.*)$/.exec(line);
    if (h?.[1]) {
      current = h[1].trim();
      out.set(current, []);
    } else if (!line.startsWith("# ")) {
      out.get(current)?.push(line);
    }
  }
  return out;
}

// paragraphs and indented code blocks of a section; tables are left out,
// because envTable reads the one the guides need
function blocks(lines: string[]): ProseBlock[] {
  const out: ProseBlock[] = [];
  let para: string[] = [];
  let code: string[] = [];
  const flush = () => {
    if (para.length) out.push({ kind: "p", text: para.join(" ") });
    if (code.length) out.push({ kind: "code", text: code.join("\n") });
    para = [];
    code = [];
  };
  for (const line of lines) {
    if (line.trim() === "" || line.startsWith("|")) {
      flush();
    } else if (line.startsWith("    ")) {
      if (para.length) flush();
      code.push(line.slice(4));
    } else {
      if (code.length) flush();
      para.push(line.trim());
    }
  }
  flush();
  return out;
}

// the readme's introduction and every section before "Environment"
export function readmeProse(md: string): ProseBlock[] {
  const out: ProseBlock[] = [];
  for (const [title, lines] of sections(md)) {
    if (title === "Environment") break;
    out.push(...blocks(lines));
  }
  return out;
}

function cells(row: string): string[] {
  return row
    .trim()
    .replace(/^\||\|$/g, "")
    .split("|")
    .map((c) => c.trim());
}

// the environment table of a readme and the paragraph right after it
export function envTable(md: string): { rows: EnvRow[]; note: string } {
  const lines = sections(md).get("Environment");
  if (!lines) throw new Error("readme has no Environment section");
  const table = lines.filter((l) => l.startsWith("|"));
  const header = table[0] ? cells(table[0]) : [];
  if (header.join(",") !== "Variable,Values,Purpose") {
    throw new Error(`unexpected environment table header: ${header.join(",")}`);
  }
  const rows = table.slice(2).map((r) => {
    const [name = "", values = "", purpose = ""] = cells(r);
    return { name: name.replace(/`/g, ""), values, purpose };
  });
  const after = blocks(lines.slice(lines.lastIndexOf(table[table.length - 1] ?? "") + 1));
  const note = after.find((b) => b.kind === "p")?.text ?? "";
  if (rows.length === 0 || note === "") throw new Error("readme environment table is incomplete");
  return { rows, note };
}

function guide(
  id: OtelGuide["id"],
  label: string,
  dir: string,
  readme: string,
  files: GuideFile[],
): OtelGuide {
  const env = envTable(readme);
  return { id, label, dir, prose: readmeProse(readme), files, env: env.rows, envNote: env.note };
}

export function otelGuides(): OtelGuide[] {
  return [
    guide("node", "Node.js", "examples/otel-node-express", nodeReadme, [
      {
        path: "package.json",
        caption: "Pinned SDK packages, and the start command that loads the SDK first.",
        code: nodePackage.trimEnd(),
      },
      {
        path: "src/telemetry.js",
        caption: "The whole SDK setup: two exporters per signal, the token on the platform copy.",
        code: nodeTelemetry.trimEnd(),
      },
    ]),
    guide("go", "Go", "examples/otel-go-nethttp", goReadme, [
      {
        path: "go.mod",
        caption: "Direct dependencies.",
        code: goRequires(goMod),
      },
      {
        path: "main.go",
        caption: "SDK setup: one tracer and one meter provider, each with two exporters.",
        code: region(goMain, "telemetry"),
      },
      {
        path: "main.go",
        caption: "Routes: the mux pattern is copied onto the server span as http.route.",
        code: region(goMain, "route"),
      },
      {
        path: "main.go",
        caption: "The instrumented handler around the mux.",
        code: region(goMain, "handler"),
      },
    ]),
    guide("python", "Python", "examples/otel-python-fastapi", pyReadme, [
      {
        path: "pyproject.toml",
        caption: "Pinned SDK packages.",
        code: region(pyProject, "dependencies"),
      },
      {
        path: "Dockerfile",
        caption: "Opt in to the stable HTTP semantic conventions.",
        code: region(pyDockerfile, "semconv"),
      },
      {
        path: "app.py",
        caption: "SDK setup: two exporters per signal, the token on the platform copy.",
        code: region(pyApp, "telemetry"),
      },
      {
        path: "app.py",
        caption: "Instrumenting the FastAPI app with both providers.",
        code: region(pyApp, "instrument"),
      },
    ]),
    guide("rust", "Rust", "examples/otel-rust-actix", rustReadme, [
      {
        path: "Cargo.toml",
        caption: "Pinned crates and the features they need.",
        code: region(rustCargo, "dependencies"),
      },
      {
        path: "src/main.rs",
        caption: "SDK setup: one tracer and one meter provider, each with two exporters.",
        code: region(rustMain, "telemetry"),
      },
      {
        path: "src/main.rs",
        caption: "The request metrics and tracing middleware around the routes.",
        code: region(rustMain, "middleware"),
      },
    ]),
    guide("java", "Java", "examples/otel-java-spring", javaReadme, [
      {
        path: "Dockerfile",
        caption: "The agent jar from Maven Central, pinned by version and checksum.",
        code: region(javaDockerfile, "agent"),
      },
      {
        path: "start.sh",
        caption: "The JVM starts with the agent and its configuration file.",
        code: region(javaStart, "run"),
      },
      {
        path: "otel.yaml",
        caption: "Agent configuration: two exporters per signal, the token on the platform copy.",
        code: region(javaConfig, "config"),
      },
      {
        path: "src/main/java/example/Application.java",
        caption: "Exit with 0 on SIGTERM, after the agent flushed both exporters.",
        code: region(javaMain, "signal"),
      },
    ]),
  ];
}

// otlp.<BASE_DOMAIN> next to the console's app.<BASE_DOMAIN>, the host layout
// every deployment uses.
// null when the console is served from another host name, because the
// endpoint cannot be derived then and guessing one would mislead.
export function otlpEndpoint(hostname: string): string | null {
  return hostname.startsWith("app.") && hostname.length > 4
    ? `https://otlp.${hostname.slice(4)}`
    : null;
}
