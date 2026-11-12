import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "@/api/endpoints";
import { enrollmentModeQuery, keepAggregatesQuery, keys } from "@/api/queries";
import type { EnrollmentMode } from "@/api/schemas";
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

export function SettingsPage() {
  return (
    <div className="space-y-8">
      <EnrollmentModeSetting />
      <KeepAggregatesSetting />
    </div>
  );
}

function EnrollmentModeSetting() {
  const mode = useQuery(enrollmentModeQuery);
  const [target, setTarget] = useState<EnrollmentMode | null>(null);
  const save = useMutation({
    mutationFn: (m: EnrollmentMode) => api.operator.setEnrollmentMode(m),
    onSuccess: async (r) => {
      queryClient.setQueryData(keys.opMode, r);
      setTarget(null);
      await queryClient.invalidateQueries({ queryKey: [...keys.operator, "audit"] });
    },
  });

  if (mode.isPending) return <Loading label="Loading the enrollment mode" />;
  if (mode.error) return <ErrorPanel error={mode.error} onRetry={() => mode.refetch()} />;
  const current = mode.data.mode;
  const other: EnrollmentMode = current === "open" ? "approval" : "open";

  return (
    <section aria-labelledby="mode-h" className="rounded-xl border bg-card p-4 sm:p-5">
      <h2 id="mode-h" className="text-lg font-semibold tracking-tight">
        Enrollment mode
      </h2>
      <p className="mt-2 text-sm">
        Current mode: <span className="font-medium">{MODE_TEXT[current].name}</span>
      </p>
      <p className="mt-1 text-sm text-muted-foreground">{MODE_TEXT[current].detail}</p>
      <Button className="mt-4" variant="outline" onClick={() => setTarget(other)}>
        Switch to {MODE_TEXT[other].name.toLowerCase()}
      </Button>
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
    </section>
  );
}

function KeepAggregatesSetting() {
  const keep = useQuery(keepAggregatesQuery);
  const [open, setOpen] = useState(false);
  const save = useMutation({
    mutationFn: (k: boolean) => api.operator.setKeepAggregates(k),
    onSuccess: async (r) => {
      queryClient.setQueryData(keys.opKeep, r);
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: [...keys.operator, "audit"] });
    },
  });

  if (keep.isPending) return <Loading label="Loading the removal setting" />;
  if (keep.error) return <ErrorPanel error={keep.error} onRetry={() => keep.refetch()} />;
  const current = keep.data.keep;

  return (
    <section aria-labelledby="keep-h" className="rounded-xl border bg-card p-4 sm:p-5">
      <h2 id="keep-h" className="text-lg font-semibold tracking-tight">
        Aggregates of removed projects
      </h2>
      <p className="mt-2 text-sm">
        When a project is removed, its anonymous daily counts are{" "}
        <span className="font-medium">{current ? "kept" : "deleted"}</span>.
      </p>
      <p className="mt-1 text-sm text-muted-foreground">
        Web dashboards, their raw data and API minute data are always deleted. The setting is copied
        into each removal when it starts, so changing it never affects a removal in progress.
      </p>
      <Button className="mt-4" variant="outline" onClick={() => setOpen(true)}>
        {current ? "Delete aggregates on removal" : "Keep aggregates on removal"}
      </Button>
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
    </section>
  );
}
