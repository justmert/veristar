import { useMutation, useQuery } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { api } from "@/api/endpoints";
import { enrollmentModeQuery, keepAggregatesQuery, keys } from "@/api/queries";
import type { EnrollmentMode } from "@/api/schemas";
import { ChoiceCard } from "@/components/choice-card";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { toast } from "@/components/toast";
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
      "New projects wait in Review. Their sites get no traffic until an operator approves them.",
  },
};

// settings that apply to every project. each change asks for a
// confirmation and goes to the audit log.
export function SettingsPage() {
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader
        title="Settings"
        description="Settings for every project. Each change asks for a confirmation and goes to the audit log."
      />
      <div className="flex max-w-prose flex-col gap-10">
        <EnrollmentModeSetting />
        <KeepAggregatesSetting />
      </div>
    </div>
  );
}

function SettingLoading({ label }: { label: string }) {
  return (
    <LoadingState label={label}>
      <Skeleton.StatRow count={2} />
    </LoadingState>
  );
}

function CurrentBadge() {
  return (
    <span className="inline-flex h-5 items-center rounded-full border border-line-strong px-2 text-caption text-fg-2">
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
        <h2 id={id} className="text-title text-fg">
          {title}
        </h2>
        <p className="text-small text-fg-2">{description}</p>
      </div>
      <div role="radiogroup" aria-labelledby={id} className="grid gap-2 sm:grid-cols-2">
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
        <span className="text-caption text-fg-2" aria-live="polite">
          {changed
            ? "You confirm the change in the next step."
            : "Choose another option to change it."}
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
      toast.success(`Enrollment is now ${MODE_TEXT[r.mode].name.toLowerCase()}.`);
      setTarget(null);
      setChoice(null);
      await queryClient.invalidateQueries({ queryKey: [...keys.operator, "audit"] });
    },
  });

  if (mode.isPending) return <SettingLoading label="Loading the enrollment mode" />;
  if (mode.error)
    return (
      <ErrorState
        title="The enrollment mode did not load"
        error={mode.error}
        onRetry={() => mode.refetch()}
      />
    );
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
          description={`${MODE_TEXT[other].detail} The change applies to new enrollments only. Enrolled projects keep their status.`}
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
      toast.success(
        r.keep
          ? "Projects removed from now on keep their daily totals."
          : "Projects removed from now on lose their daily totals.",
      );
      setOpen(false);
      setChoice(null);
      await queryClient.invalidateQueries({ queryKey: [...keys.operator, "audit"] });
    },
  });

  if (keep.isPending) return <SettingLoading label="Loading the removal setting" />;
  if (keep.error)
    return (
      <ErrorState
        title="The removal setting did not load"
        error={keep.error}
        onRetry={() => keep.refetch()}
      />
    );
  const current = keep.data.keep;
  const selected = choice ?? current;

  return (
    <SettingGroup
      id="keep-h"
      title="Daily totals of removed projects"
      description={
        <>
          A removed project now{" "}
          <span className="font-medium text-fg">{current ? "keeps" : "loses"}</span> its anonymous
          daily totals. Its dashboards, raw data and API minute data are always deleted. A removal
          that already started keeps its setting.
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
          title={
            current ? "Delete the daily totals on removal?" : "Keep the daily totals on removal?"
          }
          description={
            current
              ? "Projects removed from now on lose their anonymous daily totals too."
              : "Projects removed from now on keep their anonymous daily totals."
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
          title={k ? "Keep the daily totals" : "Delete the daily totals"}
          description={
            k
              ? "Projects removed from now on keep their anonymous daily totals."
              : "Projects removed from now on lose their anonymous daily totals too."
          }
          badge={k === current ? <CurrentBadge /> : null}
        />
      ))}
    </SettingGroup>
  );
}
