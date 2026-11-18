import { useMutation, useQuery } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { api } from "@/api/endpoints";
import { enrollmentModeQuery, keepAggregatesQuery, keys } from "@/api/queries";
import type { EnrollmentMode } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import { Segmented } from "@/components/segmented";
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

// settings that apply to every project, as rows of one list: the setting
// and what its current value does on the left, the value on the right. a
// new value opens its confirmation at once; cancelling keeps the current
// one, so the control always shows what is in force.
export function SettingsPage() {
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader title="Settings" />
      <ul className="flex min-w-0 flex-col divide-y divide-line-subtle rounded-lg border border-line bg-raised">
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

function SettingRow({
  id,
  title,
  detail,
  control,
  dialog,
}: {
  id: string;
  title: string;
  detail: ReactNode;
  control: ReactNode;
  dialog?: ReactNode;
}) {
  return (
    <section
      aria-labelledby={id}
      className="grid min-w-0 gap-x-6 gap-y-3 px-4 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:px-5"
    >
      <div className="flex min-w-0 flex-col gap-1">
        <h2 id={id} className="text-body font-medium text-fg">
          {title}
        </h2>
        <p className="text-small text-fg-2" aria-live="polite">
          {detail}
        </p>
      </div>
      {control}
      {dialog}
    </section>
  );
}

function SettingLoading({ id, title, label }: { id: string; title: string; label: string }) {
  return (
    <SettingRow
      id={id}
      title={title}
      detail={
        <LoadingState label={label}>
          <Bone aria-hidden className="h-4 w-2/3" />
        </LoadingState>
      }
      control={null}
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
      <div className="p-4">
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
      detail={MODE_TEXT[current].detail}
      control={
        <Segmented
          label="Enrollment mode"
          value={current}
          options={(["approval", "open"] as const).map((m) => ({
            value: m,
            label: MODE_TEXT[m].name,
          }))}
          onChange={(m) => {
            if (m !== current) setTarget(m);
          }}
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
      <div className="p-4">
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
      detail={current ? KEEP_TEXT.keep : KEEP_TEXT.delete}
      control={
        <Segmented
          label="Daily totals of removed projects"
          value={current ? "keep" : "delete"}
          options={[
            { value: "keep", label: "Keep" },
            { value: "delete", label: "Delete" },
          ]}
          onChange={(v) => {
            if ((v === "keep") !== current) setOpen(true);
          }}
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
