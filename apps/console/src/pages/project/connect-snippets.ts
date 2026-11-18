// the few lines a builder adds next to the exporter they already have: a
// second otlp metrics exporter that sends to veristar with the token. each
// snippet is the platform exporter of the example app for its stack
// (examples/otel-*), with the endpoint and the token written in. the whole
// example lives in the docs, one page per stack.

export type Stack = { id: "node" | "go" | "python" | "rust" | "java"; label: string };

export const STACKS: readonly Stack[] = [
  { id: "node", label: "Node.js" },
  { id: "go", label: "Go" },
  { id: "python", label: "Python" },
  { id: "rust", label: "Rust" },
  { id: "java", label: "Java" },
];

// the endpoint when the console cannot derive it from its host name
export const ENDPOINT_PLACEHOLDER = "https://otlp.<your-domain>";

export function exporterSnippet(stack: Stack["id"], endpoint: string, token: string): string {
  const url = `${endpoint}/v1/metrics`;
  switch (stack) {
    case "node":
      return [
        "new OTLPMetricExporter({",
        `  url: "${url}",`,
        `  headers: { Authorization: "Bearer ${token}" },`,
        "})",
      ].join("\n");
    case "go":
      return [
        "otlpmetrichttp.New(ctx,",
        `\totlpmetrichttp.WithEndpointURL("${url}"),`,
        `\totlpmetrichttp.WithHeaders(map[string]string{"Authorization": "Bearer ${token}"}))`,
      ].join("\n");
    case "python":
      return [
        "OTLPMetricExporter(",
        `    endpoint="${url}",`,
        `    headers={"Authorization": "Bearer ${token}"},`,
        ")",
      ].join("\n");
    case "rust":
      return [
        "MetricExporter::builder().with_http()",
        `    .with_endpoint("${url}")`,
        `    .with_headers(HashMap::from([("Authorization".to_owned(), "Bearer ${token}".to_owned())]))`,
      ].join("\n");
    case "java":
      return [
        "otlp_http:",
        `  endpoint: ${url}`,
        "  headers:",
        "    - name: Authorization",
        `      value: Bearer ${token}`,
      ].join("\n");
  }
}

// the docs page with the whole example of a stack
export function exampleDocsPath(stack: Stack["id"]): string {
  return `/onboarding/api/${stack}`;
}
