import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { Site } from "@/api/schemas";
import type { WebHourly } from "@/api/web";
import {
  combineFirstEvent,
  nextAction,
  nextText,
  type SetupStep,
  setupProgress,
  setupStatus,
  setupSteps,
  webSeen,
} from "@/lib/site-setup";
import { Step } from "@/pages/project/site-setup";
import { matchSites } from "@/pages/project/sites";
import * as f from "./fixtures/contract";
import { byRole, click, render, waitFor } from "./helpers";

const site = {
  ...(f.siteBody as Site),
  verification: "unchecked",
  last_verify_error: null,
} as Site;

function hourly(views: number[]): WebHourly {
  return {
    day: "2026-11-18",
    band: "primary",
    source: "engine",
    sites: [],
    points: views.map((v, i) => ({
      at: `2026-11-18T${String(i).padStart(2, "0")}:00:00Z`,
      pageviews: v,
      visitors: v,
    })),
  };
}

describe("first event from either source", () => {
  it("completes on the gateway or on the web engine, whichever sees it first", () => {
    expect(combineFirstEvent("received", false)).toBe("received");
    expect(combineFirstEvent("waiting", true)).toBe("received");
    expect(combineFirstEvent(null, true)).toBe("received");
    expect(combineFirstEvent("refused", true)).toBe("received");
  });

  it("keeps the gateway's refusal and waits while nothing is seen", () => {
    expect(combineFirstEvent("refused", false)).toBe("refused");
    expect(combineFirstEvent("waiting", null)).toBe("waiting");
    expect(combineFirstEvent(null, false)).toBe("waiting");
    expect(combineFirstEvent(null, null)).toBeNull();
  });

  it("reads a page view of the engine as seen", () => {
    expect(webSeen(undefined)).toBe(false);
    expect(webSeen(hourly([0, 0]))).toBe(false);
    expect(webSeen(hourly([0, 3]))).toBe(true);
  });
});

describe("the setup model", () => {
  it("starts at the tag and moves to the domain once an event arrived", () => {
    const before = setupSteps(site, "waiting");
    expect(before.map((s) => s.state)).toEqual(["current", "todo", "todo", "todo"]);
    expect(setupProgress(before)).toEqual({ done: 0, total: 3 });
    const after = setupSteps(site, "received");
    expect(after.map((s) => s.state)).toEqual(["done", "done", "current", "todo"]);
    expect(setupProgress(after)).toEqual({ done: 2, total: 3 });
  });

  it("marks a refusal and a failed domain check as needing action", () => {
    expect(setupSteps(site, "refused")[1]?.state).toBe("attention");
    const failed = { ...site, last_verify_error: "dns_no_record" } as Site;
    expect(setupSteps(failed, "received")[2]?.state).toBe("attention");
    expect(setupStatus(failed, "received")).toBe("failed");
  });

  it("keeps a later step neutral while an earlier one is open", () => {
    const failed = { ...site, last_verify_error: "dns_no_record" } as Site;
    expect(setupSteps(failed, "waiting")[2]?.state).toBe("todo");
  });

  it("counts stellar.toml as optional", () => {
    const done = { ...site, verification: "verified" } as Site;
    const steps = setupSteps(done, "received");
    expect(steps.find((s) => s.key === "toml")?.optional).toBe(true);
    expect(setupProgress(steps)).toEqual({ done: 3, total: 3 });
    expect(setupStatus(done, "received")).toBe("done");
    expect(nextText("done")).toBe("Setup is complete.");
  });

  it("gives one status and one next action per site", () => {
    expect(setupStatus(site, null)).toBe("checking");
    expect(setupStatus(site, "waiting")).toBe("waiting");
    expect(nextAction(site, "waiting").label).toBe("Continue setup");
    expect(nextAction(site, "unverified")).toEqual({
      label: "Verify domain",
      to: `/projects/${site.tenant_id}/sites/${site.id}#domain`,
    });
    expect(nextAction(site, "done").to).toBe(`/projects/${site.tenant_id}/web?site=${site.id}`);
  });
});

describe("sites list", () => {
  it("finds sites by name or domain", () => {
    const list = [site, { ...site, id: "b", name: "Docs", domain: "docs.example.org" }];
    expect(matchSites(list, "DOCS").map((s) => s.id)).toEqual(["b"]);
    expect(matchSites(list, "example.org").length).toBe(2);
  });
});

describe("a setup step", () => {
  function Harness({ start, settled = true }: { start: SetupStep["state"]; settled?: boolean }) {
    const [state, setState] = useState(start);
    const [known, setKnown] = useState(settled);
    return (
      <>
        <button type="button" onClick={() => setState("done")}>
          finish
        </button>
        <button type="button" onClick={() => setState("current")}>
          undo
        </button>
        <button type="button" onClick={() => setKnown(true)}>
          settle
        </button>
        <ol>
          <Step
            n={1}
            step={{ key: "install", title: "Install the tag", state, optional: false }}
            id="install"
            settled={known}
          >
            <p>copy this snippet</p>
          </Step>
        </ol>
      </>
    );
  }
  const toggle = (host: HTMLElement) =>
    host.querySelector("li button[aria-expanded]") as HTMLButtonElement | null;

  it("folds a step that is done when the page opens", async () => {
    const host = await render(<Harness start="done" />);
    expect(host.textContent).not.toContain("copy this snippet");
    expect(toggle(host)?.textContent).toBe("Show");
    await click(toggle(host) as HTMLElement);
    expect(host.textContent).toContain("copy this snippet");
    expect(toggle(host)?.getAttribute("aria-expanded")).toBe("true");
  });

  it("folds once the state is known on load, not before", async () => {
    const host = await render(<Harness start="done" settled={false} />);
    // the state is not known yet: the step does not fold, the body stays
    expect(host.textContent).toContain("copy this snippet");
    await click(byRole(host, "button", "settle"));
    await waitFor(() => expect(host.textContent).not.toContain("copy this snippet"));
    expect(toggle(host)?.textContent).toBe("Show");
  });

  it("keeps a step open that becomes done while the person works in it", async () => {
    const host = await render(<Harness start="current" />);
    expect(toggle(host)).toBeNull();
    await click(byRole(host, "button", "finish"));
    // done now, but the body stays where the person was working
    expect(host.querySelector('li[data-state="done"]')).not.toBeNull();
    expect(host.textContent).toContain("copy this snippet");
    expect(toggle(host)?.textContent).toBe("Hide");
    // the person folds it on purpose
    await click(toggle(host) as HTMLElement);
    await waitFor(() => expect(host.textContent).not.toContain("copy this snippet"));
    expect(toggle(host)?.textContent).toBe("Show");
    // a step that is no longer done shows its body again
    await click(byRole(host, "button", "undo"));
    expect(host.textContent).toContain("copy this snippet");
    expect(toggle(host)).toBeNull();
  });
});
