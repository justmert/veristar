import { describe, expect, it } from "vitest";
import { ApiError } from "@/api/client";
import { errorEnvelope } from "@/api/schemas";
import { AccountPage, blockingProjects, confirmsLogin } from "@/pages/account";
import * as f from "./fixtures/contract";
import { byLabel, byRole, click, renderRoutes, stubFetch, type, waitFor } from "./helpers";

const soleOwner = {
  error: {
    code: "sole_owner",
    message:
      "you are the only owner of a project; make another member an owner, or ask the operators to remove the project, first",
    tenants: [{ id: f.TENANT_ID, name: "Stellar Wallet" }],
    request_id: f.USER_ID,
  },
};

function page() {
  return renderRoutes(
    [
      { path: "/account", element: <AccountPage /> },
      { path: "/sign-in", element: <p>sign-in page</p> },
      { path: "/projects/:tenantId/members", element: <p>members page</p> },
    ],
    "/account",
  );
}

function confirmButton(): HTMLButtonElement {
  const dialog = document.body.querySelector<HTMLElement>("dialog[open]");
  if (!dialog) throw new Error("no dialog");
  return byRole(dialog, "button", "Delete account") as HTMLButtonElement;
}

describe("account deletion (contract 4.1.6)", () => {
  it("accepts the login typed exactly, around spaces only", () => {
    expect(confirmsLogin("octocat", "octocat")).toBe(true);
    expect(confirmsLogin("  octocat ", "octocat")).toBe(true);
    expect(confirmsLogin("Octocat", "octocat")).toBe(false);
    expect(confirmsLogin("octo", "octocat")).toBe(false);
    expect(confirmsLogin("", "octocat")).toBe(false);
  });

  it("parses the sole_owner envelope with its projects", () => {
    const parsed = errorEnvelope.parse(soleOwner);
    expect(parsed.error.tenants).toEqual([{ id: f.TENANT_ID, name: "Stellar Wallet" }]);
    const err = new ApiError({
      status: 409,
      code: "sole_owner",
      message: "",
      tenants: parsed.error.tenants,
    });
    expect(blockingProjects(err)).toEqual([{ id: f.TENANT_ID, name: "Stellar Wallet" }]);
    expect(
      blockingProjects(new ApiError({ status: 409, code: "last_owner", message: "" })),
    ).toEqual([]);
  });

  it("shows the signed-in account and keeps the delete button off until the login is typed", async () => {
    stubFetch([{ status: 200, body: f.meBody }]);
    const host = await page();
    await waitFor(() => expect(host.textContent).toContain("octocat"));
    expect(host.textContent).toContain("1234567");
    await click(byRole(host, "button", "Delete account"));
    await waitFor(() => expect(confirmButton().disabled).toBe(true));
    await type(byLabel(document.body, "Type octocat to confirm"), "octo");
    expect(confirmButton().disabled).toBe(true);
    await type(byLabel(document.body, "Type octocat to confirm"), "octocat");
    expect(confirmButton().disabled).toBe(false);
  });

  it("sends DELETE /api/me with the console header and goes to sign-in", async () => {
    const calls = stubFetch([{ status: 200, body: f.meBody }, { status: 204 }]);
    const host = await page();
    await waitFor(() => expect(host.textContent).toContain("octocat"));
    await click(byRole(host, "button", "Delete account"));
    await type(byLabel(document.body, "Type octocat to confirm"), "octocat");
    await click(confirmButton());
    await waitFor(() => expect(host.textContent).toContain("sign-in page"));
    expect(calls[1]?.method).toBe("DELETE");
    expect(calls[1]?.url).toBe("/api/me");
    expect(calls[1]?.headers.get("X-Requested-With")).toBe("sa-console");
  });

  it("lists the projects the person owns alone, with links to their members", async () => {
    stubFetch([
      { status: 200, body: f.meBody },
      { status: 409, body: soleOwner },
    ]);
    const host = await page();
    await waitFor(() => expect(host.textContent).toContain("octocat"));
    await click(byRole(host, "button", "Delete account"));
    await type(byLabel(document.body, "Type octocat to confirm"), "octocat");
    await click(confirmButton());
    await waitFor(() =>
      expect(host.textContent).toContain("You are the only owner of this project"),
    );
    const link = [...host.querySelectorAll("a")].find((a) => a.textContent === "Stellar Wallet");
    expect(link?.getAttribute("href")).toBe(`/projects/${f.TENANT_ID}/members`);
    expect(document.body.querySelector("dialog[open]")).toBeNull();
  });
});
