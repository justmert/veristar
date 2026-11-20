import { useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { type ReactNode, useState } from "react";
import { api } from "@/api/endpoints";
import { enrollmentModeQuery, keepAggregatesQuery, keys } from "@/api/queries";
import type { EnrollmentMode } from "@/api/schemas";
import { ChoiceCard } from "@/components/choice-card";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import { ErrorState, LoadingState } from "@/components/states";
import { toast } from "@/components/toast";
import { Skeleton as Bone } from "@/components/ui/skeleton";
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

// settings that apply to every project, as rows of one list: the setting on
// the left, its two options on the right. a new value opens its
// confirmation at once; cancelling keeps the current one, so the control
// always shows what is in force.
export function SettingsPage() {
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader title="Settings" />
      <ul className="flex min-w-0 flex-col divide-y divide-line">
        <li>
          <EnrollmentModeSetting />
        </li>
        <li>
          <KeepAggregatesSetting />
        </li>
      </ul>
    </div>
  );
}

// one setting as a pair of options, each with its own effect, so the effect
// of the other choice is known before it is clicked. the chosen one is
// marked; choosing the other asks once, with the same words.
function SettingRow({
  id,
  title,
  control,
  dialog,
}: {
  id: string;
  title: string;
  control: ReactNode;
  dialog?: ReactNode;
}) {
  return (
    <section
      aria-labelledby={id}
      className="grid min-w-0 gap-x-6 gap-y-3 py-5 first:pt-2 md:grid-cols-[minmax(0,17rem)_minmax(0,1fr)]"
    >
      <h2 id={id} className="pt-3 text-body font-medium text-fg max-md:pt-0">
        {title}
      </h2>
      {control}
      {dialog}
    </section>
  );
}

function OptionPair<T extends string>({
  name,
  label,
  value,
  options,
  onPick,
}: {
  name: string;
  label: string;
  value: T;
  options: { value: T; title: string; detail: string }[];
  onPick: (v: T) => void;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="grid min-w-0 gap-2.5 sm:grid-cols-2">
      {options.map((o) => (
        <ChoiceCard
          key={o.value}
          name={name}
          value={o.value}
          checked={o.value === value}
          onChange={() => {
            if (o.value !== value) onPick(o.value);
          }}
          title={o.title}
          description={o.detail}
          className={cn("p-4", o.value === value ? "border-line-focus" : "bg-sunken")}
        />
      ))}
    </div>
  );
}

function SettingLoading({ id, title, label }: { id: string; title: string; label: string }) {
  return (
    <SettingRow
      id={id}
      title={title}
      control={
        <LoadingState label={label}>
          <Bone aria-hidden className="h-20 w-full" />
        </LoadingState>
      }
    />
  );
}

function EnrollmentModeSetting() {
  const mode = useQuery(enrollmentModeQuery);
  const [target, setTarget] = useState<EnrollmentMode | null>(null);
  const save = useMutation({
    mutationFn: (m: EnrollmentMode) => api.operator.setEnrollmentMode(m),
    onSuccess: async (r) => {
      queryClient.setQueryData(keys.opMode, r);
      toast.success(
        r.mode === "approval"
          ? "Enrollment now requires approval."
          : "Enrollment is now open. New projects are active at once.",
      );
      setTarget(null);
      await queryClient.invalidateQueries({ queryKey: [...keys.operator, "audit"] });
    },
  });

  if (mode.isPending)
    return (
      <SettingLoading id="mode-h" title="Enrollment mode" label="Loading the enrollment mode" />
    );
  if (mode.error)
    return (
      <div className="py-5">
        <ErrorState
          compact
          title="The enrollment mode did not load"
          error={mode.error}
          onRetry={() => mode.refetch()}
        />
      </div>
    );
  const current = mode.data.mode;
  const other: EnrollmentMode = current === "open" ? "approval" : "open";

  return (
    <SettingRow
      id="mode-h"
      title="Enrollment mode"
      control={
        <OptionPair
          name="enrollment-mode"
          label="Enrollment mode"
          value={current}
          options={(["open", "approval"] as const).map((m) => ({
            value: m,
            title: MODE_TEXT[m].name,
            detail: MODE_TEXT[m].detail,
          }))}
          onPick={setTarget}
        />
      }
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
    />
  );
}

const KEEP_TEXT = {
  keep: "Projects removed from now on keep their anonymous daily totals.",
  delete: "Projects removed from now on lose their anonymous daily totals too.",
};

function KeepAggregatesSetting() {
  const keep = useQuery(keepAggregatesQuery);
  const [open, setOpen] = useState(false);
  const save = useMutation({
    mutationFn: (k: boolean) => api.operator.setKeepAggregates(k),
    onSuccess: async (r) => {
      queryClient.setQueryData(keys.opKeep, r);
      toast.success(r.keep ? KEEP_TEXT.keep : KEEP_TEXT.delete);
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: [...keys.operator, "audit"] });
    },
  });

  if (keep.isPending)
    return (
      <SettingLoading
        id="keep-h"
        title="Daily totals of removed projects"
        label="Loading the removal setting"
      />
    );
  if (keep.error)
    return (
      <div className="py-5">
        <ErrorState
          compact
          title="The removal setting did not load"
          error={keep.error}
          onRetry={() => keep.refetch()}
        />
      </div>
    );
  const current = keep.data.keep;

  return (
    <SettingRow
      id="keep-h"
      title="Daily totals of removed projects"
      control={
        <OptionPair
          name="keep-aggregates"
          label="Daily totals of removed projects"
          value={current ? "keep" : "delete"}
          options={[
            { value: "keep", title: "Keep", detail: KEEP_TEXT.keep },
            { value: "delete", title: "Delete", detail: KEEP_TEXT.delete },
          ]}
          onPick={() => setOpen(true)}
        />
      }
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
          description={`${current ? KEEP_TEXT.delete : KEEP_TEXT.keep} Dashboards, raw data and API minute data are always deleted. A removal that already started keeps its setting.`}
          confirmLabel="Change setting"
          pendingLabel="Saving"
          tone="primary"
          pending={save.isPending}
          error={save.error}
          onConfirm={() => save.mutate(!current)}
        />
      }
    />
  );
}
