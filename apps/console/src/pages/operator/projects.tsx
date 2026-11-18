import { useSearchParams } from "react-router";
import { Segmented } from "@/components/segmented";
import { GlobalApiPage } from "./global-api";
import { GlobalWebPage } from "./global-web";
import { TenantsPage } from "./tenants";

export type ProjectsView = "status" | "web" | "api";

const VIEWS: readonly { value: ProjectsView; label: string }[] = [
  { value: "status", label: "Status" },
  { value: "web", label: "Web" },
  { value: "api", label: "API" },
];

export function readView(v: string | null): ProjectsView {
  return v === "web" || v === "api" ? v : "status";
}

// every enrolled project in one list with three views: its status, its web
// numbers and its api numbers over a range of utc days. the view is in the
// address (?view=), so a view can be shared and reloaded.
export function OperatorProjectsPage() {
  const [params, setParams] = useSearchParams();
  const view = readView(params.get("view"));
  return (
    <div className="flex flex-col gap-6">
      <Segmented
        label="View"
        value={view}
        options={VIEWS}
        onChange={(v) => setParams(v === "status" ? {} : { view: v }, { replace: true })}
      />
      {view === "web" ? <GlobalWebPage /> : view === "api" ? <GlobalApiPage /> : <TenantsPage />}
    </div>
  );
}
