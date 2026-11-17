import { describe, expect, it } from "vitest";
import { ApiError } from "@/api/client";
import { errorText } from "@/lib/errors";
import { retryDelay, retryQuery } from "@/query-client";

const err = (status: number, code: string, retryAfter: number | null = null) =>
  new ApiError({ status, code, message: "", retryAfter });

describe("query retries", () => {
  it("keeps asking while analytics access is being set up", () => {
    const pending = err(409, "provisioning_pending");
    expect(retryQuery(0, pending)).toBe(true);
    expect(retryQuery(10, pending)).toBe(true);
    expect(retryQuery(30, pending)).toBe(false);
    expect(retryDelay(0, pending)).toBe(10_000);
    expect(retryDelay(0, err(409, "provisioning_pending", 5))).toBe(5_000);
    expect(errorText(pending)).toContain("being set up");
    expect(errorText(pending)).not.toContain("unavailable");
  });

  it("does not repeat other client errors and tries 5xx twice more", () => {
    expect(retryQuery(0, err(404, "not_found"))).toBe(false);
    expect(retryQuery(0, err(409, "site_removed"))).toBe(false);
    expect(retryQuery(1, err(503, "unavailable"))).toBe(true);
    expect(retryQuery(2, err(503, "unavailable"))).toBe(false);
  });

  it("names the wait of a rate limit in words", () => {
    expect(errorText(err(429, "rate_limited", 30))).toBe(
      "Too many requests. Try again in 30 seconds.",
    );
    expect(errorText(err(429, "rate_limited", 600))).toBe(
      "Too many requests. Try again in 10 minutes.",
    );
    expect(errorText(err(429, "rate_limited"))).toBe("Too many requests. Try again in a minute.");
  });
});
