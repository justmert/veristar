import { afterEach, describe, expect, it, vi } from "vitest";
import { OperatorPasskeys, passkeyNameError } from "@/components/operator-passkeys";
import { toast } from "@/components/toast";
import { byRole, click, pickRowAction, render, stubFetch, waitFor } from "./helpers";

const KEY = {
  id: "0b8d7c1e-3f2a-4e5b-9c6d-7e8f9a0b1c2d",
  name: "Laptop",
  created_at: "2026-11-01T10:00:00Z",
  last_used_at: null,
};

afterEach(() => vi.restoreAllMocks());

describe("your passkeys", () => {
  it("takes a name of 1 to 64 characters", () => {
    expect(passkeyNameError("")).toBe("Enter a name.");
    expect(passkeyNameError("   ")).toBe("Enter a name.");
    expect(passkeyNameError("Laptop")).toBeNull();
    expect(passkeyNameError("x".repeat(65))).toBe("Use at most 64 characters.");
  });

  it("lists the passkeys and confirms a delete with a toast", async () => {
    const said = vi.spyOn(toast, "success");
    const calls = stubFetch([
      { status: 200, body: { items: [KEY], sign_in_passkey: true } },
      { status: 204 },
      { status: 200, body: { items: [], sign_in_passkey: true } },
    ]);
    const host = await render(<OperatorPasskeys userName="octocat" />);
    await waitFor(() => expect(host.textContent).toContain("Laptop"));
    expect(host.textContent).toContain("Your sign-in passkey");
    await pickRowAction(host, "Laptop", "Delete");
    const dialog = document.body.querySelector<HTMLElement>("dialog[open]");
    if (!dialog) throw new Error("no dialog");
    await click(byRole(dialog, "button", "Delete passkey"));
    await waitFor(() => expect(said).toHaveBeenCalledWith("Laptop was deleted."));
    expect(calls[1]?.method).toBe("DELETE");
    expect(calls[1]?.url).toContain(`/operator/passkeys/${KEY.id}`);
  });
});
