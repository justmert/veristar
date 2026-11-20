---
title: "Java"
description: "Dual export from a Java service on Spring Boot with the OpenTelemetry Java agent."
icon: "java"
iconType: "brands"
---

Dual export from a Java service, without changing its code. Built from the example
[`examples/otel-java-spring`](https://github.com/justmert/veristar/tree/main/examples/otel-java-spring): Spring Boot 4.1.2 on Java 25
(Eclipse Temurin 25.0.5) with the OpenTelemetry Java agent 2.33.1, which bundles the OpenTelemetry
Java SDK 1.67.0. `tests/otelapps` runs it against stock collectors in every export mode, and the
`apipath` tag runs it against the real platform collector. Read the
[API quickstart](quickstart.md) first for the token and the environment variables.

The agent instruments Spring MVC, Tomcat, JDBC, HTTP clients and over a hundred other libraries
when the JVM starts. The same setup works for any Java service the agent supports; for the Stellar
Anchor Platform, see [anchor-platform.md](anchor-platform.md).

For production, send the agent's metrics through an OpenTelemetry Collector that forwards to the
platform with HTTP/2 health pings ([collector-fanout.md](collector-fanout.md)). The agent's own
HTTP/2 sender has no health check, so after a silent network drop its exports time out for
minutes before it reconnects; the Collector notices the dead connection within seconds
([troubleshooting](troubleshooting.md#exports-time-out-until-the-app-restarts)).

## Attach the agent

Download the agent jar from Maven Central and verify its checksum. The example's image does both
in one step:

{/* include examples/otel-java-spring/Dockerfile region=agent */}
```dockerfile
# the agent jar from maven central, pinned by version and its sha256
ADD --chmod=444 --checksum=sha256:e43a270df53c6f423e0ab157be9c27ccc01d5dc3232e162fe54777573db2f149 \
    https://repo1.maven.org/maven2/io/opentelemetry/javaagent/opentelemetry-javaagent/2.33.1/opentelemetry-javaagent-2.33.1.jar \
    /app/opentelemetry-javaagent.jar
```

Start the JVM with `-javaagent` and point the agent at its configuration file with
`-Dotel.config.file`:

{/* include examples/otel-java-spring/start.sh region=run */}
```sh
# exec, so the jvm is the process that receives sigterm
exec java -javaagent:/app/opentelemetry-javaagent.jar -Dotel.config.file="$config" -jar /app/app.jar
```

## Two exporters per signal

The agent's `OTEL_EXPORTER_OTLP_*` variables take one endpoint per signal. For two exporters per
signal, give the agent a declarative configuration file: each tracer processor and each metric
reader has its own exporter, and only the platform's carries the token. The agent supports the file
from version 2.26.0 and documents it as experimental, so pin the agent version, as the example does.

{/* include examples/otel-java-spring/otel.yaml region=config */}
```yaml
file_format: "1.0"
resource:
  attributes:
    - name: service.name
      value: ${OTEL_SERVICE_NAME:-otel-java-spring}
  attributes_list: ${OTEL_RESOURCE_ATTRIBUTES}
propagator:
  composite:
    - tracecontext:
    - baggage:
tracer_provider:
  processors:
    - batch:
        exporter:
          otlp_http:
            endpoint: ${TENANT_OTLP_ENDPOINT}/v1/traces
    - batch:
        exporter:
          otlp_http:
            endpoint: ${PLATFORM_OTLP_ENDPOINT}/v1/traces
            headers:
              - name: Authorization
                value: Bearer ${PLATFORM_OTLP_TOKEN}
meter_provider:
  readers:
    - periodic:
        interval: ${METRICS_EXPORT_INTERVAL_MS:-10000}
        exporter:
          otlp_http:
            endpoint: ${TENANT_OTLP_ENDPOINT}/v1/metrics
            temporality_preference: ${OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE:-cumulative}
            default_histogram_aggregation: ${OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION:-explicit_bucket_histogram}
    - periodic:
        interval: ${METRICS_EXPORT_INTERVAL_MS:-10000}
        exporter:
          otlp_http:
            endpoint: ${PLATFORM_OTLP_ENDPOINT}/v1/metrics
            headers:
              - name: Authorization
                value: Bearer ${PLATFORM_OTLP_TOKEN}
            temporality_preference: ${OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE:-cumulative}
            default_histogram_aggregation: ${OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION:-explicit_bucket_histogram}
```

With the file, the agent takes its SDK settings from the file only and ignores variables such as
`OTEL_EXPORTER_OTLP_ENDPOINT`. `${VAR}` and `${VAR:-default}` in the file are replaced from the
environment, so the token stays out of the file. The file has no `logger_provider`, so the agent
exports no logs; add one with your own backend's exporter to keep them.

`temporality_preference` takes `cumulative` or `delta`, `default_histogram_aggregation` takes
`explicit_bucket_histogram` or `base2_exponential_bucket_histogram`; the platform accepts every
combination.

## Metrics to the platform, traces to one backend

When your own backend needs traces only, or no telemetry at all from this service, the agent's
variables are enough: metrics and traces each have their own endpoint, and each signal goes to one
backend. [anchor-platform.md](anchor-platform.md#configure-the-agent) shows this setup, tested with
the Stellar Anchor Platform. Write the space in `Bearer <token>` as `%20`, because the agent
URL-decodes header values.

## Dependencies

The agent instruments the HTTP server without any dependency. The example adds
`opentelemetry-api` only for its own internal span, at the version the agent bridges:

{/* include examples/otel-java-spring/pom.xml region=dependencies */}
```xml
<properties>
  <java.version>25</java.version>
  <!-- the api version the agent 2.33.1 bridges (it targets sdk 1.67.0); the
       spring boot parent manages an older one, so it is set here -->
  <opentelemetry.version>1.67.0</opentelemetry.version>
</properties>

<dependencies>
  <dependency>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-webmvc</artifactId>
  </dependency>
  <!-- only for the app's own span; the agent instruments the http server
       without any dependency -->
  <dependency>
    <groupId>io.opentelemetry</groupId>
    <artifactId>opentelemetry-api</artifactId>
  </dependency>
</dependencies>
```

## Routes and health checks

The Spring MVC instrumentation sets `http.route` to the mapping template (`/accounts/{id}`) on the
request metric and the server span; see [http-route.md](http-route.md). A request no controller
matches is recorded under Spring's resource handler pattern `/**`, never under the raw path.

The agent records every request Tomcat serves, health checks included. Samplers drop spans but not
the request metric, so the example does not filter them. The platform marks health routes such as
`/healthz`, `/health` and `/actuator/health/liveness` as infrastructure traffic and leaves them out
of request counts, error rates and latency
([what-gets-dropped.md](what-gets-dropped.md#changed-before-storing)).

## Shutdown

The agent exports the last interval of both providers in a JVM shutdown hook. Make the JVM the
process that receives SIGTERM: start it with `exec` from a shell script, or as the container's
entrypoint. The JVM exits with 143 after SIGTERM; the example exits with 0 instead, so container
runtimes report a clean stop:

{/* include examples/otel-java-spring/src/main/java/example/Application.java region=signal */}
```java
// the jvm exits with 143 on sigterm. exiting with 0 instead runs the
// same shutdown hooks (spring's graceful shutdown and the agent's flush of
// both exporters) and tells the container runtime the stop was clean
Signal.handle(new Signal("TERM"), signal -> System.exit(0));
```

## Export modes the tests cover

| Variable | Values |
|---|---|
| `EXPORT_SIGNALS` | `metrics,traces` (default) or `traces` (the agent starts with `otel-traces.yaml`, which has no meter provider) |
| `METRICS_TEMPORALITY` | `cumulative` (default) or `delta` |
| `METRICS_HISTOGRAM` | `explicit` (default) or `exponential` |
