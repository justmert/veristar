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
      // go's http/2 client keeps a dead connection for minutes; the health
      // pings close it, so the snippet carries them and a copy is safe as is
      return [
        "tr := http.DefaultTransport.(*http.Transport).Clone()",
        "tr.HTTP2 = &http.HTTP2Config{SendPingTimeout: 15 * time.Second, PingTimeout: 5 * time.Second}",
        "otlpmetrichttp.New(ctx,",
        `\totlpmetrichttp.WithEndpointURL("${url}"),`,
        `\totlpmetrichttp.WithHeaders(map[string]string{"Authorization": "Bearer ${token}"}),`,
        "\totlpmetrichttp.WithHTTPClient(&http.Client{Transport: tr, Timeout: 10 * time.Second}))",
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
