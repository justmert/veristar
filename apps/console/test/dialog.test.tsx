import { act, useState } from "react";
import { describe, expect, it } from "vitest";
import { Dialog } from "@/components/ui/dialog";
import { render, waitFor } from "./helpers";

// a caller that refuses to close while something runs, as the passkey and
// wallet dialogs do during a ceremony
function Busy({ busy }: { busy: boolean }) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <p data-testid="state">{open ? "open" : "closed"}</p>
      <Dialog
        open={open}
        onOpenChange={(o) => {
          if (busy) return;
          setOpen(o);
        }}
        title="Working"
      />
    </>
  );
}

function dialogEl(host: HTMLElement): HTMLDialogElement {
  const d = host.querySelector("dialog");
  if (!d) throw new Error("no dialog");
  return d;
}

describe("dialog escape", () => {
  it("stays open when the caller refuses to close", async () => {
    const host = await render(<Busy busy />);
    const d = dialogEl(host);
    await act(async () => {
      d.dispatchEvent(new Event("cancel", { cancelable: true }));
    });
    expect(d.open).toBe(true);
    // a browser that closes anyway gets the dialog shown again
    await act(async () => {
      d.close();
    });
    await waitFor(() => expect(d.open).toBe(true));
    expect(host.querySelector("[data-testid=state]")?.textContent).toBe("open");
  });

  it("closes when the caller agrees", async () => {
    const host = await render(<Busy busy={false} />);
    const d = dialogEl(host);
    await act(async () => {
      d.dispatchEvent(new Event("cancel", { cancelable: true }));
    });
    await waitFor(() => expect(d.open).toBe(false));
    expect(host.querySelector("[data-testid=state]")?.textContent).toBe("closed");
  });
});
