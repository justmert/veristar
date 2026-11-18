# otel-anchor-platform

The Stellar Anchor Platform 5.0.0 (`stellar/anchor-platform`) with the OpenTelemetry Java agent 2.33.1 attached, so its SEP server and platform server send request metrics to Veristar without code changes. Traces go to the anchor's own OTLP backend, logs nowhere.

- `Dockerfile` adds the agent jar, pinned by version and checksum, to the Anchor Platform image and starts the JVM with `-javaagent`.
- `compose.yaml` runs the SEP server and the platform server in separate containers on PostgreSQL, each with its own `OTEL_SERVICE_NAME`, and sets the agent's `OTEL_*` variables.
- `anchor.env` and `config/` hold a local Anchor Platform configuration with SEP-1, SEP-10 and SEP-24 for native XLM.

## Environment

| Variable | Values | Purpose |
|---|---|---|
| `TENANT_OTLP_ENDPOINT` | base URL, required | the anchor's own trace backend, e.g. `http://otel-collector:4318` |
| `PLATFORM_OTLP_ENDPOINT` | base URL, required | Veristar, `https://otlp.<BASE_DOMAIN>` |
| `PLATFORM_OTLP_TOKEN` | `sa_otlp_...`, required | sent as `Authorization: Bearer <token>` with the metrics |
| `METRICS_EXPORT_INTERVAL_MS` | default `60000` | the agent's metric export interval |
| `ANCHOR_SEP10_SIGNING_SEED` | `S...`, required | the SEP-10 signing key of the local anchor |
| `ANCHOR_JWT_SECRET` | 32 bytes or more, required | the local anchor's SEP-10 and SEP-24 JWT secret |
| `ANCHOR_DB_PASSWORD` | required | the local PostgreSQL password |

The agent sends metrics only to `PLATFORM_OTLP_ENDPOINT` and traces only to `TENANT_OTLP_ENDPOINT`. When the anchor keeps no traces, change `OTEL_TRACES_EXPORTER` in `compose.yaml` to `none`.

`tests/otelapps` runs this file against stock collectors (`go test -tags otelapps -run TestAnchorPlatform ./tests/otelapps/`): it creates SEP-24 deposits, reads them through the SEP and platform APIs and checks every route and status that reaches the platform stand-in.

    docker compose up -d --build
