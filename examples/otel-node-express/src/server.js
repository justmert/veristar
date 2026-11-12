"use strict";

const express = require("express");
const { trace, SpanKind } = require("@opentelemetry/api");
const config = require("./config");
const { sdk } = require("./telemetry");

const tracer = trace.getTracer("otel-node-express");
const accounts = new Map();
const payments = new Map();
let nextId = 0;

// lookup runs in an internal span with an event. both carry marker strings so
// the platform tests can prove non-server spans and events never leave its
// collector, while the tenant backend still receives them.
function lookup(id) {
  return tracer.startActiveSpan("SA_MARKER_internal_span", { kind: SpanKind.INTERNAL }, (span) => {
    span.addEvent("SA_MARKER_span_event", { "account.lookup": id });
    const account = accounts.get(id);
    span.end();
    return account;
  });
}

// a positive decimal with up to 7 fractional digits, the stellar amount precision
const amountPattern = /^\d+(\.\d{1,7})?$/;

const app = express();
app.use(express.json());

app.get("/healthz", (_req, res) => res.status(204).end());

app.post("/accounts", (req, res) => {
  const name = typeof req.body?.name === "string" ? req.body.name.trim() : "";
  if (!name) return res.status(400).json({ error: "name is required" });
  nextId += 1;
  const account = { id: String(nextId), name };
  accounts.set(account.id, account);
  payments.set(account.id, []);
  res.status(201).json(account);
});

app.get("/accounts/:id", (req, res) => {
  const account = lookup(req.params.id);
  if (!account) return res.status(404).json({ error: "account not found" });
  res.json(account);
});

app.post("/accounts/:id/payments", (req, res) => {
  if (!lookup(req.params.id)) return res.status(404).json({ error: "account not found" });
  const amount = req.body?.amount;
  if (typeof amount !== "string" || !amountPattern.test(amount) || Number(amount) <= 0) {
    return res.status(400).json({ error: "amount must be a positive decimal" });
  }
  const list = payments.get(req.params.id);
  const payment = { id: String(list.length + 1), amount };
  list.push(payment);
  res.status(201).json(payment);
});

app.get("/accounts/:id/payments", (req, res) => {
  if (!lookup(req.params.id)) return res.status(404).json({ error: "account not found" });
  res.json({ payments: payments.get(req.params.id) });
});

const server = app.listen(config.port, () => {
  console.log(
    `listening on :${config.port} metrics=${config.metricsEnabled} delta=${config.delta} exponential=${config.exponential} interval=${config.interval}ms`,
  );
});

// flush after the server stopped accepting requests so every request is exported
function stop(signal) {
  console.log(`${signal} received, draining`);
  server.close(() => {
    sdk.shutdown().then(
      () => {
        console.log("telemetry flushed, exiting");
        process.exit(0);
      },
      (err) => {
        console.error("telemetry shutdown failed", err);
        process.exit(1);
      },
    );
  });
  server.closeIdleConnections();
}
process.on("SIGTERM", stop);
process.on("SIGINT", stop);
