import { act, useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "@/api/client";
import type { OperatorTenantDetail } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DecisionPanel } from "@/components/decision-panel";
import { EvidenceList, evidenceOf } from "@/components/evidence-list";
import { PageHeader } from "@/components/page-header";
import { SecretPanel } from "@/components/secret-panel";
import { StatCard } from "@/components/stat-card";
import { EmptyState, ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { Toaster, toast } from "@/components/toast";
import { errorDetails, errorMessage } from "@/lib/errors";
import * as f from "./fixtures/contract";
import { byLabel, byRole, click, render, stubFetch, type, waitFor } from "./helpers";

const apiError = new ApiError({
  status: 500,
  code: "internal",
  message: "failed at /api/tenants/0193a6f3-0101-7a22-b3c4-d5e6f7a8b9c0/sites",
  requestId: "req-42",
});

describe("page header", () => {
  it("draws the title, purpose, freshness and actions, primary last", async () => {
    const host = await render(
      <PageHeader
        title="Web"
        description="Visits to your sites."
        freshness={{ at: new Date(), cadence: "Counts refresh every 5 minutes." }}
        primary={<button type="button">Add a site</button>}
        secondary={<button type="button">Export CSV</button>}
      />,
    );
    expect(host.querySelector("h1")?.textContent).toBe("Web");
    expect(host.querySelector("h1")?.className).toContain("text-heading");
    expect(host.querySelector("[data-freshness]")?.textContent).toMatch(/^Updated \d\d:\d\d UTC$/);
    expect(host.textContent).toContain("Counts refresh every 5 minutes.");
    const labels = [...host.querySelectorAll("button")].map((b) => b.textContent);
    expect(labels).toEqual(["Export CSV", "Add a site"]);
    expect(host.querySelector("nav")).toBeNull();
  });
});

describe("states", () => {
  it("keeps transport detail out of the message and puts it in Copy details", async () => {
    expect(errorMessage(apiError)).toBe("failed at");
    expect(errorMessage(new ApiError({ status: 502, code: "x", message: "" }))).toBe(
      "The request failed.",
    );
    const bare = new ApiError({
      status: 500,
      code: "unexpected_response",
      message: "/operator/audit?limit=50 answered 500 without an error body",
    });
    expect(errorMessage(bare)).toBe("The service answered with an error. Try again.");
    expect(errorDetails(bare)).toContain("/operator/audit?limit=50");
    const details = errorDetails(apiError, new Date("2026-11-18T01:49:00Z"));
    expect(details).toContain("Request id: req-42");
    expect(details).toContain("Error code: internal");
    expect(details).toContain("Time: 2026-11-18T01:49:00.000Z");

    const retry = vi.fn();
    const host = await render(
      <ErrorState title="The sites did not load" error={apiError} onRetry={retry} />,
    );
    const alert = host.querySelector("[role=alert]") as HTMLElement;
    expect(alert.textContent).not.toContain("req-42");
    expect(alert.textContent).not.toContain("0193a6f3");
    expect(alert.textContent).not.toContain("/api/");
    await click(byRole(host, "button", "Try again"));
    expect(retry).toHaveBeenCalledOnce();
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    await click(byRole(host, "button", "Copy details"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Request id: req-42"));
  });

  it("draws empty and loading states", async () => {
    const host = await render(
      <>
        <EmptyState title="No sites yet" action={<button type="button">Add a site</button>}>
          Add a site to see its traffic.
        </EmptyState>
        <LoadingState label="Loading web analytics">
          <Skeleton.StatRow count={4} />
          <Skeleton.Chart />
          <Skeleton.Table rows={3} columns={3} />
        </LoadingState>
      </>,
    );
    expect(host.querySelector("[role=status]")?.textContent).toContain("No sites yet");
    const loading = host.querySelector("[aria-busy=true]") as HTMLElement;
    expect(loading.textContent).toBe("Loading web analytics");
    expect(loading.querySelectorAll("[data-slot=skeleton]").length).toBeGreaterThan(10);
  });

  it("says why a value is missing, never n/a", async () => {
    const host = await render(<Missing reason="Too few visits" />);
    expect(host.textContent).toBe("–Too few visits");
    expect(host.textContent).not.toContain("n/a");
  });
});

describe("toast", () => {
  it("announces a message politely and leaves when dismissed", async () => {
    const host = await render(<Toaster />);
    await act(async () => {
      toast.success("Site added.");
    });
    const live = host.querySelector("[aria-live=polite]") as HTMLElement;
    expect(live.textContent).toContain("Site added.");
    const dismiss = host.querySelector("[data-toast=success] button") as HTMLButtonElement;
    await click(dismiss);
    expect(live.textContent).not.toContain("Site added.");
    await act(async () => toast.clear());
    expect(live.textContent).toBe("");
  });

  it("shows one toast at a time: a new one replaces the last", async () => {
    const host = await render(<Toaster />);
    await act(async () => {
      toast.success("Site added.");
      toast.error("The key was not revoked. Try again.", {
        action: { label: "Retry", onClick: () => {} },
      });
    });
    expect(host.querySelectorAll("[data-toast]")).toHaveLength(1);
    expect(host.querySelector("[data-toast=error]")?.textContent).toContain("Retry");
    await act(async () => toast.clear());
  });
});

function Confirm(props: Partial<Parameters<typeof ConfirmDialog>[0]>) {
  const [open, setOpen] = useState(true);
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={setOpen}
      title="Reset all web data of example.org?"
      description="Every event of the site is deleted. This cannot be undone."
      confirmLabel="Reset site data"
      pending={false}
      error={null}
      onConfirm={() => {}}
      {...props}
    />
  );
}

describe("confirm dialog", () => {
  it("asks for a reason when it is required", async () => {
    const onConfirm = vi.fn();
    const host = await render(<Confirm reason={{ required: true }} onConfirm={onConfirm} />);
    await click(byRole(host, "button", "Reset site data"));
    expect(onConfirm).not.toHaveBeenCalled();
    expect(host.textContent).toContain("Required.");
    await type(byLabel(host, "Reason"), "  Spam traffic ");
    await click(byRole(host, "button", "Reset site data"));
    expect(onConfirm).toHaveBeenCalledWith("Spam traffic");
  });

  it("keeps the confirm button off until the typed text matches", async () => {
    const onConfirm = vi.fn();
    const host = await render(
      <Confirm
        typed={{ value: "example.org", label: "Type the site's domain to confirm" }}
        onConfirm={onConfirm}
      />,
    );
    const confirm = byRole(host, "button", "Reset site data") as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    await type(byLabel(host, "Type the site's domain"), "example.org");
    expect(confirm.disabled).toBe(false);
    await click(confirm);
    expect(onConfirm).toHaveBeenCalledWith("");
  });

  it("draws a destructive confirm next to Cancel", async () => {
    const host = await render(<Confirm />);
    expect(byRole(host, "button", "Cancel").getAttribute("type")).toBe("button");
    expect(byRole(host, "button", "Reset site data").getAttribute("data-variant")).toBe(
      "destructive",
    );
  });
});

describe("secret panel", () => {
  it("masks the secret and needs the stored confirmation before Done", async () => {
    const done = vi.fn();
    const host = await render(
      <SecretPanel
        value="sa_otlp_Ab3xSecretValue"
        noun="token"
        name="Payments API"
        onDone={done}
      />,
    );
    expect(host.textContent).not.toContain("Ab3xSecretValue");
    const finish = byRole(host, "button", "Done") as HTMLButtonElement;
    expect(finish.disabled).toBe(true);
    await click(host.querySelector('[aria-label="Show the secret"]') as HTMLElement);
    await waitFor(() => expect(host.textContent).toContain("sa_otlp_Ab3xSecretValue"));
    await click(host.querySelector('[role="checkbox"]') as HTMLElement);
    await click(finish);
    expect(done).toHaveBeenCalledOnce();
  });
});

describe("stat card", () => {
  it("shows the value, change and selected state, and a reason for a missing value", async () => {
    const select = vi.fn();
    const host = await render(
      <>
        <StatCard
          label="Page views"
          value={69798}
          change={{ text: "+12%", direction: "up", good: true, period: "previous 30 days" }}
          selected
          onSelect={select}
          data-tile="views"
        />
        <StatCard label="Bounce rate" value={null} missingReason="Too few visits" />
        <StatCard label="Visits" value={1} state="loading" />
        <StatCard label="Visitors" value={1} state="error" />
      </>,
    );
    const views = host.querySelector("[data-tile=views]") as HTMLButtonElement;
    expect(views.getAttribute("aria-pressed")).toBe("true");
    expect(views.textContent).toContain("69,798");
    expect(views.textContent).toContain("+12%vs previous 30 days");
    expect(views.textContent).toContain("In chart");
    await click(views);
    expect(select).toHaveBeenCalledOnce();
    expect(host.textContent).toContain("–Too few visits");
    expect(host.textContent).toContain("Loading Visits");
    expect(host.textContent).toContain("Did not load");
  });
});

describe("status badge", () => {
  it("says each status in one word set, amber only where an operator acts", async () => {
    const host = await render(
      <>
        <StatusBadge kind="project" status="pending" />
        <StatusBadge kind="project" status="pending" operator />
        <StatusBadge kind="site" status="active" />
        <StatusBadge kind="key" status="revoked" />
        <StatusBadge kind="alert" status="page" />
        <StatusBadge kind="grade" grade={null} />
        <StatusBadge kind="grade" grade="A" />
      </>,
    );
    const words = [...host.querySelectorAll("[data-status]")].map((s) => s.textContent);
    expect(words).toEqual([
      "In review",
      "In review",
      "Active",
      "Revoked",
      "Act now",
      "Not graded",
      "Grade A",
    ]);
    const dots = [...host.querySelectorAll("span[aria-hidden].rounded-full")].map(
      (d) => d.className,
    );
    expect(dots[0]).toContain("bg-info");
    expect(dots[1]).toContain("bg-warning");
    expect(host.querySelector("[data-grade=none]")).not.toBeNull();
    expect(host.textContent).not.toContain("Grade not graded");
  });
});

const detail = f.operatorDetailBody as unknown as OperatorTenantDetail;

describe("evidence list", () => {
  it("lists the signals with words and the grade", async () => {
    const host = await render(<EvidenceList items={evidenceOf(detail, { grade: "B" })} />);
    const list = host.querySelector("ul[aria-label=Evidence]") as HTMLElement;
    expect(list.textContent).toContain("Domain");
    expect(list.textContent).toContain("stellar.toml");
    expect(list.textContent).toContain("SCF round");
    expect(list.textContent).toContain("Integrity grade:Grade B");
    const item = (k: string) => list.querySelector(`[data-evidence=${k}]`) as HTMLElement;
    expect(item("domain").dataset.signal).toBe("ok");
    expect(item("domain").textContent).toContain("Verified (1 of 1 site)");
    expect(item("grade").dataset.signal).toBe("partial");
  });

  it("states a failed or partial check in words, never as a pass", async () => {
    const site = f.operatorDetailBody.sites[0];
    const many = {
      ...detail,
      sites: [
        { ...site, id: "s1", verification: "unverified", stellar_toml: null },
        { ...site, id: "s2", verification: "unverified", stellar_toml: null },
      ],
    } as unknown as OperatorTenantDetail;
    const none = await render(<EvidenceList items={evidenceOf(many)} />);
    const domain = none.querySelector("[data-evidence=domain]") as HTMLElement;
    expect(domain.dataset.signal).toBe("missing");
    expect(domain.textContent).toContain("Not verified (0 of 2 sites)");
    const some = {
      ...many,
      sites: [{ ...site, id: "s1", verification: "verified" }, ...many.sites.slice(1)],
    } as unknown as OperatorTenantDetail;
    const part = await render(<EvidenceList items={evidenceOf(some)} />);
    const d2 = part.querySelector("[data-evidence=domain]") as HTMLElement;
    expect(d2.dataset.signal).toBe("partial");
    expect(d2.textContent).toContain("1 of 2 sites verified");
  });
});

describe("decision panel", () => {
  it("decides a site with the same words and a toast", async () => {
    const calls = stubFetch([
      { status: 200, body: f.operatorSiteBody },
      { status: 200, body: f.operatorDetailBody },
    ]);
    const host = await render(
      <>
        <DecisionPanel subject={{ kind: "site", id: f.SITE_ID, name: "example.org" }} />
        <Toaster />
      </>,
    );
    // nothing is chosen on open: the submit waits for a choice, and ctrl
    // enter sends nothing
    expect(byRole(host, "radio", "ApproveA").getAttribute("aria-checked")).toBe("false");
    expect(byRole(host, "radio", "RejectR").getAttribute("aria-checked")).toBe("false");
    const send = host.querySelector("button[type=submit]") as HTMLButtonElement;
    expect(send.disabled).toBe(true);
    expect(send.textContent).toBe("Choose Approve or Reject");
    await act(async () => {
      host
        .querySelector("form")
        ?.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Enter", ctrlKey: true, bubbles: true }),
        );
    });
    expect(calls).toHaveLength(0);
    await act(async () => {
      host
        .querySelector("form")
        ?.dispatchEvent(new KeyboardEvent("keydown", { key: "r", bubbles: true }));
    });
    expect(byRole(host, "radio", "RejectR").getAttribute("aria-checked")).toBe("true");
    await click(host.querySelector("button[type=submit]") as HTMLElement);
    expect(host.textContent).toContain("Required.");
    await type(byLabel(host, "Reason"), "Not our domain.");
    await click(host.querySelector("button[type=submit]") as HTMLElement);
    await waitFor(() => expect(host.textContent).toContain("example.org rejected."));
    expect(calls[0]?.url).toBe(`/api/operator/sites/${f.SITE_ID}/reject`);
    expect(calls[0]?.body).toEqual({ reason: "Not our domain." });
  });
});
