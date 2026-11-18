import { describe, expect, it } from "vitest";
import { otlpEndpoint } from "@/lib/otlp-endpoint";

describe("otlpEndpoint", () => {
  it("derives otlp.<domain> from app.<domain>", () => {
    expect(otlpEndpoint("app.sa-test.example")).toBe("https://otlp.sa-test.example");
  });

  it("derives nothing from another host name", () => {
    expect(otlpEndpoint("localhost")).toBeNull();
    expect(otlpEndpoint("console.example.org")).toBeNull();
  });
});
