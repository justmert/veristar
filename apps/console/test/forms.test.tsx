import { describe, expect, it } from "vitest";
import { EnrollForm } from "@/pages/enroll";
import { AddMemberForm } from "@/pages/members";
import { AddSiteForm } from "@/pages/sites";
import * as f from "./fixtures/contract";
import { byLabel, byRole, click, render, stubFetch, type, waitFor } from "./helpers";

function errorOf(host: HTMLElement, label: string): string | null {
  const el = byLabel(host, label);
  const ids = (el.getAttribute("aria-describedby") ?? "").split(" ").filter(Boolean);
  const texts = ids
    .map((id) => host.ownerDocument.getElementById(id))
    .filter((n) => n?.getAttribute("role") === "alert")
    .map((n) => n?.textContent ?? "");
  return texts[0] ?? null;
}

describe("enrollment form", () => {
  it("checks every required field before sending anything", async () => {
    const calls = stubFetch([]);
    const host = await render(<EnrollForm termsVersion={f.TERMS_VERSION} />);
    await click(byRole(host, "button", "Enroll project"));
    expect(calls).toHaveLength(0);
    expect(errorOf(host, "Project name")).toBe("Required.");
    expect(errorOf(host, "Website")).toBe("Required.");
    expect(errorOf(host, "How does the project use Stellar?")).toBe("Required.");
    expect(host.textContent).toContain("Accept the terms to enroll.");
    expect(byLabel(host, "Project name").getAttribute("aria-invalid")).toBe("true");
    expect(document.activeElement).toBe(byLabel(host, "Project name"));
  });

  it("names the account id that is not a valid strkey", async () => {
    stubFetch([]);
    const host = await render(<EnrollForm termsVersion={f.TERMS_VERSION} />);
    await type(byLabel(host, "Account or contract ids"), `${f.G_KEY}\n${f.G_BAD_CHECKSUM}`);
    await type(byLabel(host, "Website"), "http://example.org");
    await click(byRole(host, "button", "Enroll project"));
    expect(errorOf(host, "Account or contract ids")).toContain(f.G_BAD_CHECKSUM);
    expect(errorOf(host, "Website")).toBe("Enter a full https:// address.");
  });

  it("posts the contract's enrollment body with the accepted terms version", async () => {
    const calls = stubFetch([{ status: 201, body: { ...f.tenantBody, provisioning: "pending" } }]);
    const host = await render(<EnrollForm termsVersion={f.TERMS_VERSION} />, "/projects/new");
    await type(byLabel(host, "Project name"), "  Stellar DEX ");
    await type(byLabel(host, "Website"), "https://example.org");
    await type(byLabel(host, "How does the project use Stellar?"), "Soroban AMM on mainnet");
    await type(byLabel(host, "Account or contract ids"), `${f.G_KEY}, ${f.C_KEY}`);
    await click(byLabel(host, "I accept the") as unknown as HTMLElement);
    await click(byRole(host, "button", "Enroll project"));
    await waitFor(() => expect(host.textContent).toContain("sites page"));
    expect(calls).toHaveLength(1);
    expect(calls[0]?.url).toBe("/api/tenants");
    expect(calls[0]?.method).toBe("POST");
    expect(calls[0]?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(calls[0]?.body).toEqual({
      name: "Stellar DEX",
      enrollment: {
        website: "https://example.org",
        stellar_usage: "Soroban AMM on mainnet",
        stellar_ids: [f.G_KEY, f.C_KEY],
      },
      accepted_terms_version: f.TERMS_VERSION,
    });
  });

  it("puts the server's 422 field codes and 409 slug_taken on the fields", async () => {
    stubFetch([
      {
        status: 422,
        body: f.errorBody("validation_failed", {
          "enrollment.stellar_ids[1]": "invalid_strkey",
          "enrollment.website": "not_https_url",
        }),
      },
      { status: 409, body: f.errorBody("slug_taken") },
    ]);
    const host = await render(<EnrollForm termsVersion={f.TERMS_VERSION} />);
    await type(byLabel(host, "Project name"), "Stellar DEX");
    await type(byLabel(host, "Project address"), "stellar-dex");
    await type(byLabel(host, "Website"), "https://example.org");
    await type(byLabel(host, "How does the project use Stellar?"), "AMM");
    await type(byLabel(host, "Account or contract ids"), `${f.G_KEY}\n${f.C_KEY}`);
    await click(byLabel(host, "I accept the") as unknown as HTMLElement);
    await click(byRole(host, "button", "Enroll project"));
    await waitFor(() => expect(errorOf(host, "Account or contract ids")).toContain(f.C_KEY));
    expect(errorOf(host, "Website")).toBe("Enter a full https:// address.");

    await click(byRole(host, "button", "Enroll project"));
    await waitFor(() =>
      expect(errorOf(host, "Project address")).toBe("This address is taken. Choose another one."),
    );
  });
});

describe("add site form", () => {
  it("offers the domain as first origin and posts the site", async () => {
    const calls = stubFetch([{ status: 201, body: f.siteBody }]);
    let done = false;
    const host = await render(
      <AddSiteForm
        tenantId={f.TENANT_ID}
        onDone={() => {
          done = true;
        }}
        cancellable={false}
      />,
    );
    await type(byLabel(host, "Name"), "Marketing site");
    await type(byLabel(host, "Domain"), "Example.org");
    await click(byRole(host, "button", "Use https://example.org"));
    expect(byLabel(host, "Allowed origins").value).toBe("https://example.org");
    await click(byRole(host, "button", "Add site"));
    await waitFor(() => expect(done).toBe(true));
    expect(calls[0]?.url).toBe(`/api/tenants/${f.TENANT_ID}/sites`);
    expect(calls[0]?.body).toEqual({
      name: "Marketing site",
      domain: "example.org",
      origins: ["https://example.org"],
    });
  });

  it("refuses origins outside the domain before sending", async () => {
    const calls = stubFetch([]);
    const host = await render(
      <AddSiteForm tenantId={f.TENANT_ID} onDone={() => {}} cancellable={false} />,
    );
    await type(byLabel(host, "Name"), "Docs");
    await type(byLabel(host, "Domain"), "example.org");
    await type(byLabel(host, "Allowed origins"), "https://example.org\nhttps://other.net");
    await click(byRole(host, "button", "Add site"));
    expect(calls).toHaveLength(0);
    expect(errorOf(host, "Allowed origins")).toBe(
      "https://other.net: The host must be the site's domain or one of its subdomains.",
    );
  });

  it("shows the server's per-origin code on the origins box", async () => {
    stubFetch([{ status: 422, body: f.validationErrorBody }]);
    const host = await render(
      <AddSiteForm tenantId={f.TENANT_ID} onDone={() => {}} cancellable={false} />,
    );
    await type(byLabel(host, "Name"), "Docs");
    await type(byLabel(host, "Domain"), "example.org");
    await type(byLabel(host, "Allowed origins"), "https://example.org\nhttps://www.example.org");
    await click(byRole(host, "button", "Add site"));
    await waitFor(() =>
      expect(errorOf(host, "Allowed origins")).toBe(
        "https://www.example.org: Enter only scheme and host, like https://example.org, with no path.",
      ),
    );
  });
});

describe("add member form", () => {
  it("explains an invite for a person without an account", async () => {
    const calls = stubFetch([
      { status: 201, body: f.addMemberAsInviteBody },
      { status: 200, body: f.membersBody },
    ]);
    const host = await render(<AddMemberForm tenantId={f.TENANT_ID} />);
    await type(byLabel(host, "GitHub username"), "@someone");
    await click(byRole(host, "button", "Add"));
    await waitFor(() => expect(host.textContent).toContain("someone has no account yet"));
    expect(calls[0]?.body).toEqual({ github_login: "someone", role: "member" });
  });

  it("refuses an invalid login and maps already_member", async () => {
    stubFetch([{ status: 409, body: f.errorBody("already_member") }]);
    const host = await render(<AddMemberForm tenantId={f.TENANT_ID} />);
    await type(byLabel(host, "GitHub username"), "bad--login");
    await click(byRole(host, "button", "Add"));
    expect(errorOf(host, "GitHub username")).toBe("Not a valid GitHub username.");
    await type(byLabel(host, "GitHub username"), "octocat");
    await click(byRole(host, "button", "Add"));
    await waitFor(() =>
      expect(errorOf(host, "GitHub username")).toBe("Already a member of this project."),
    );
  });
});
