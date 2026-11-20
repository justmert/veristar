import { act, useState } from "react";
import { describe, expect, it } from "vitest";
import { CreateDialog, type CreatedSecret } from "@/components/create-dialog";
import { byRole, click, render } from "./helpers";

function Harness() {
  const [secret, setSecret] = useState<CreatedSecret | null>(null);
  return (
    <CreateDialog
      label="Create key"
      pending={false}
      error={null}
      onSubmit={() => setSecret({ value: "vs_key_secret", noun: "key", name: "warehouse" })}
      secret={secret}
      onClosed={() => setSecret(null)}
    >
      <input aria-label="Name" />
    </CreateDialog>
  );
}

describe("create dialog", () => {
  it("opens the form from the header button and shows the secret in the same dialog", async () => {
    const host = await render(<Harness />);
    await click(byRole(host, "button", "Create key"));
    const dialog = document.body.querySelector<HTMLElement>("dialog[open]");
    if (!dialog) throw new Error("no dialog");
    expect(dialog.querySelector("input[aria-label=Name]")).not.toBeNull();
    const submit = dialog.querySelector<HTMLButtonElement>("button[type=submit]");
    await click(submit as HTMLElement);
    // the same dialog now holds the secret, and closes only once stored
    expect(dialog.hasAttribute("open")).toBe(true);
    expect(dialog.textContent).toContain("Copy your new key");
    expect(dialog.querySelector("input[aria-label=Name]")).toBeNull();
    const done = byRole(dialog, "button", "Done") as HTMLButtonElement;
    expect(done.disabled).toBe(true);
    // escape asks to close; the dialog has no corner close button
    await act(async () => {
      dialog.dispatchEvent(new Event("cancel", { cancelable: true }));
    });
    expect(dialog.hasAttribute("open")).toBe(true);
    await click(dialog.querySelector('button[role="checkbox"]') as HTMLElement);
    expect(done.disabled).toBe(false);
    await click(done);
    expect(dialog.hasAttribute("open")).toBe(false);
  });
});
