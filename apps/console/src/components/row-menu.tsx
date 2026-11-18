import { MoreHorizontal } from "lucide-react";
import { type ReactNode, useState } from "react";
import {
  ConfirmDialog,
  type ReasonOptions,
  type TypedConfirmation,
} from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";

// the confirmation a destructive row action asks for before it runs.
// `run` does the work; the dialog shows its progress and its error, and
// closes once it resolves
export type RowConfirm = {
  title: string;
  description: ReactNode;
  confirmLabel: string;
  pendingLabel?: string;
  reason?: ReasonOptions;
  typed?: TypedConfirmation;
  run: (reason: string) => Promise<unknown>;
};

export type RowAction = {
  label: string;
  icon?: ReactNode;
  // destructive actions are drawn in critical text and always come last,
  // after a separator
  destructive?: boolean;
  disabled?: boolean;
  // for a destructive action, the confirmation it opens. without it the
  // action calls onSelect (which then opens the caller's own dialog)
  confirm?: RowConfirm;
  onSelect?: () => void;
};

// the one place for a row's secondary and destructive actions: a "..."
// button that opens a menu. the everyday actions first, the destructive
// ones last in critical text, each behind a confirmation. a row without
// any action draws nothing. `name` names the row for the button's label.
export function RowMenu({
  name,
  actions,
  className,
}: {
  name: string;
  actions: (RowAction | null | false | undefined)[];
  className?: string;
}) {
  const [confirming, setConfirming] = useState<RowConfirm | null>(null);
  // the dialog keeps its text while it closes
  const [shown, setShown] = useState<RowConfirm | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const all = actions.filter((a): a is RowAction => !!a);
  if (all.length === 0) return null;
  const everyday = all.filter((a) => !a.destructive);
  const destructive = all.filter((a) => a.destructive);

  function pick(a: RowAction) {
    if (a.confirm) {
      setError(null);
      setConfirming(a.confirm);
      setShown(a.confirm);
      return;
    }
    a.onSelect?.();
  }

  async function run(reason: string) {
    if (!confirming) return;
    setPending(true);
    setError(null);
    try {
      await confirming.run(reason);
      setConfirming(null);
    } catch (e) {
      setError(e);
    } finally {
      setPending(false);
    }
  }

  const item = (a: RowAction) => (
    <MenuItem
      key={a.label}
      tone={a.destructive ? "critical" : undefined}
      disabled={a.disabled}
      onSelect={() => pick(a)}
    >
      {a.icon}
      {a.label}
    </MenuItem>
  );

  return (
    <>
      <Menu>
        <MenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`Actions for ${name}`}
            data-row-menu=""
            className={className ?? "max-lg:size-(--vs-control-touch)"}
          >
            <MoreHorizontal aria-hidden />
          </Button>
        </MenuTrigger>
        <MenuContent align="end">
          {everyday.map(item)}
          {everyday.length > 0 && destructive.length > 0 ? <MenuSeparator /> : null}
          {destructive.map(item)}
        </MenuContent>
      </Menu>
      {destructive.some((a) => a.confirm) ? (
        <ConfirmDialog
          open={confirming !== null}
          onOpenChange={(o) => {
            if (!o && !pending) setConfirming(null);
          }}
          title={shown?.title ?? ""}
          description={shown?.description ?? ""}
          confirmLabel={shown?.confirmLabel ?? ""}
          pendingLabel={shown?.pendingLabel}
          reason={shown?.reason}
          typed={shown?.typed}
          pending={pending}
          error={error}
          onConfirm={run}
        />
      ) : null}
    </>
  );
}
