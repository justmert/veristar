import { useMutation, useQuery } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { api } from "@/api/endpoints";
import { enrollmentModeQuery, keepAggregatesQuery, keys } from "@/api/queries";
import type { EnrollmentMode } from "@/api/schemas";
import { ChoiceCard } from "@/components/choice-card";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { Button } from "@/components/ui/button";
import { queryClient } from "@/query-client";

const MODE_TEXT: Record<EnrollmentMode, { name: string; detail: string }> = {
  open: {
    name: "Open",
    detail: "New projects are active at once and their sites receive traffic right away.",
  },
  approval: {
    name: "Approval required",
    detail:
      "New projects wait in the review queue. Nothing is set up for them and their sites receive no traffic until an operator approves them.",
  },
};

// the operator layout draws the place header and its tabs; this page starts
// with its own section header
export function SettingsPage() {
  return (
    <div className="flex max-w-[56rem] animate-rise-in flex-col gap-8">
      <header className="flex flex-col gap-1">
        <h2 className="text-section text-fg">Settings</h2>
        <p className="max-w-prose text-body text-fg-2">
          Platform settings for every project. Each change asks for confirmation and is recorded in
          the audit log.
        </p>
      </header>
      <EnrollmentModeSetting />
      <KeepAggregatesSetting />
    </div>
  );
}

function CurrentBadge() {
  return (
    <span className="inline-flex h-5 items-center rounded-full border border-line-strong px-2 text-axis font-medium text-fg-2">
      Current
    </span>
  );
}

// one setting: a heading, what it governs, the options as radio cards and a
// change button that is live only when another option is chosen
function SettingGroup({
  id,
  title,
  description,
  children,
  changed,
  onChange,
  dialog,
}: {
  id: string;
  title: string;
  description: ReactNode;
  children: ReactNode;
  changed: boolean;
  onChange: () => void;
  dialog: ReactNode;
}) {
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h3 id={id} className="text-title text-fg">
          {title}
        </h3>
        <p className="text-small text-fg-2">{description}</p>
      </div>
      <div role="radiogroup" aria-labelledby={id} className="grid gap-2.5 sm:grid-cols-2">
        {children}
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Button
          variant="outline"
          size="sm"
          disabled={!changed}
          className="max-lg:h-(--vs-control-touch)"
          onClick={onChange}
        >
          Change setting
        </Button>
        <span className="text-caption font-normal text-fg-3">
          {changed ? "Asks for confirmation. Recorded in the audit log." : "No change selected."}
        </span>
      </div>
      {dialog}
    </section>
  );
}

function EnrollmentModeSetting() {
  const mode = useQuery(enrollmentModeQuery);
  const [choice, setChoice] = useState<EnrollmentMode | null>(null);
  const [target, setTarget] = useState<EnrollmentMode | null>(null);
  const save = useMutation({
    mutationFn: (m: EnrollmentMode) => api.operator.setEnrollmentMode(m),
    onSuccess: async (r) => {
      queryClient.setQueryData(keys.opMode, r);
      setTarget(null);
      setChoice(null);
      await queryClient.invalidateQueries({ queryKey: [...keys.operator, "audit"] });
    },
  });

  if (mode.isPending) return <Loading label="Loading the enrollment mode" />;
  if (mode.error) return <ErrorPanel error={mode.error} onRetry={() => mode.refetch()} />;
  const current = mode.data.mode;
  const other: EnrollmentMode = current === "open" ? "approval" : "open";
  const selected = choice ?? current;

  return (
    <SettingGroup
      id="mode-h"
      title="Enrollment mode"
      description={
        <>
          Current mode: <span className="font-medium text-fg">{MODE_TEXT[current].name}</span>.
          Applies to projects enrolled from now on.
        </>
      }
      changed={selected !== current}
      onChange={() => setTarget(other)}
      dialog={
        <ConfirmDialog
          open={target !== null}
          onOpenChange={(o) => {
            if (!o) {
              setTarget(null);
              save.reset();
            }
          }}
          title={`Switch enrollment to ${MODE_TEXT[other].name.toLowerCase()}?`}
          description={`${MODE_TEXT[other].detail} The switch applies to new enrollments only; existing projects keep their status.`}
          confirmLabel="Switch mode"
          pendingLabel="Switching"
          tone="primary"
          pending={save.isPending}
          error={save.error}
          onConfirm={() => target && save.mutate(target)}
        />
      }
    >
      {(["approval", "open"] as const).map((m) => (
        <ChoiceCard
          key={m}
          name="enrollment-mode"
          value={m}
          checked={selected === m}
          onChange={() => setChoice(m)}
          title={MODE_TEXT[m].name}
          description={MODE_TEXT[m].detail}
          badge={m === current ? <CurrentBadge /> : null}
        />
      ))}
    </SettingGroup>
  );
}

function KeepAggregatesSetting() {
  const keep = useQuery(keepAggregatesQuery);
  const [choice, setChoice] = useState<boolean | null>(null);
  const [open, setOpen] = useState(false);
  const save = useMutation({
    mutationFn: (k: boolean) => api.operator.setKeepAggregates(k),
    onSuccess: async (r) => {
      queryClient.setQueryData(keys.opKeep, r);
      setOpen(false);
      setChoice(null);
      await queryClient.invalidateQueries({ queryKey: [...keys.operator, "audit"] });
    },
  });

  if (keep.isPending) return <Loading label="Loading the removal setting" />;
  if (keep.error) return <ErrorPanel error={keep.error} onRetry={() => keep.refetch()} />;
  const current = keep.data.keep;
  const selected = choice ?? current;

  return (
    <SettingGroup
      id="keep-h"
      title="Aggregates of removed projects"
      description={
        <>
          When a project is removed, its anonymous daily counts are{" "}
          <span className="font-medium text-fg">{current ? "kept" : "deleted"}</span>. Web
          dashboards, their raw data and API minute data are always deleted. The setting is copied
          into each removal when it starts, so changing it never affects a removal in progress.
        </>
      }
      changed={selected !== current}
      onChange={() => setOpen(true)}
      dialog={
        <ConfirmDialog
          open={open}
          onOpenChange={(o) => {
            setOpen(o);
            if (!o) save.reset();
          }}
          title={current ? "Delete aggregates on removal?" : "Keep aggregates on removal?"}
          description={
            current
              ? "Projects removed from now on lose their anonymous daily counts as well."
              : "Projects removed from now on keep their anonymous daily counts after removal."
          }
          confirmLabel="Change setting"
          pendingLabel="Saving"
          tone="primary"
          pending={save.isPending}
          error={save.error}
          onConfirm={() => save.mutate(!current)}
        />
      }
    >
      {[true, false].map((k) => (
        <ChoiceCard
          key={String(k)}
          name="keep-aggregates"
          value={k ? "keep" : "delete"}
          checked={selected === k}
          onChange={() => setChoice(k)}
          title={k ? "Keep aggregates on removal" : "Delete aggregates on removal"}
          description={
            k
              ? "Projects removed from now on keep their anonymous daily counts after removal."
              : "Projects removed from now on lose their anonymous daily counts as well."
          }
          badge={k === current ? <CurrentBadge /> : null}
        />
      ))}
    </SettingGroup>
  );
}
