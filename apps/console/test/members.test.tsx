import { Outlet } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Member, Tenant } from "@/api/schemas";
import { toast } from "@/components/toast";
import { addResultText, MembersPage, matchMembers } from "@/pages/project/members";
import * as f from "./fixtures/contract";
import { byRole, click, renderRoutes, stubFetch, waitFor } from "./helpers";

const owner = f.membersBody.members[0] as Member;
const path = `/projects/${f.TENANT_ID}/members`;

function routes() {
  const ctx = { tenant: f.tenantBody as Tenant, me: f.meBody, isOwner: true, termsCurrent: true };
  return [
    {
      path: "/projects/:tenantId",
      element: <Outlet context={ctx} />,
      children: [{ path: "members", element: <MembersPage /> }],
    },
  ];
}

afterEach(() => vi.restoreAllMocks());

describe("members", () => {
  it("finds people by name, login or address", () => {
    const list = [
      owner,
      {
        ...owner,
        user_id: "b",
        github_login: null,
        display_name: "GABC…XYZ",
        stellar_address: "GABCDEF",
      },
    ];
    expect(matchMembers(list, "").length).toBe(2);
    expect(matchMembers(list, "gabcdef").map((m) => m.user_id)).toEqual(["b"]);
    expect(matchMembers(list, "nobody")).toEqual([]);
  });

  it("says who was added and how an invite works", () => {
    expect(addResultText(f.addMemberAsMemberBody as never)).toBe("hubot is added as member.");
    expect(addResultText(f.addMemberAsInviteBody as never)).toContain("has no account yet");
  });

  it("changes a role only after the confirmation, with a toast", async () => {
    const said = vi.spyOn(toast, "success");
    const second = {
      ...owner,
      user_id: f.OTHER_USER_ID,
      github_login: "hubot",
      display_name: "hubot",
      role: "member",
    };
    const calls = stubFetch([
      { status: 200, body: { members: [owner, second], invites: [] } },
      { status: 200, body: { ...second, role: "owner" } },
      { status: 200, body: { members: [owner, { ...second, role: "owner" }], invites: [] } },
    ]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("hubot"));
    const select = host.querySelector<HTMLSelectElement>('select[aria-label="Role of hubot"]');
    if (!select) throw new Error("no role select");
    const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set;
    setter?.call(select, "owner");
    select.dispatchEvent(new Event("change", { bubbles: true }));
    const dialog = document.body.querySelector<HTMLElement>("dialog[open]");
    if (!dialog) throw new Error("no dialog");
    expect(dialog.textContent).toContain("Make hubot an owner?");
    expect(calls).toHaveLength(1);
    await click(byRole(dialog, "button", "Change role"));
    await waitFor(() => expect(said).toHaveBeenCalledWith("hubot is now an owner."));
    expect(calls[1]?.method).toBe("PATCH");
  });

  it("lists open invites in the same table, counted in the header", async () => {
    stubFetch([{ status: 200, body: f.membersBody }]);
    const host = await renderRoutes(routes(), path);
    const n = f.membersBody.invites.length;
    await waitFor(() => expect(host.textContent).toContain(n === 1 ? "1 invite" : `${n} invites`));
    expect(host.textContent).toContain("Invited");
    expect(host.querySelectorAll("[data-invite]").length).toBeGreaterThan(0);
    expect(host.querySelector(".text-warning")).toBeNull();
    // row actions sit in a menu; no red button stands in a row
    expect(host.querySelector("[data-invite] button.text-critical")).toBeNull();
    expect(host.querySelector("[data-invite] [data-row-menu]")).not.toBeNull();
  });
});
