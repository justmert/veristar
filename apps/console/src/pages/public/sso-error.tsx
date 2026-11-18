import { Link2Off } from "lucide-react";
import { PublicFrame } from "@/components/public-frame";
import { ProjectsButton, StopPage } from "./not-found";

// the address an older version of the console sent people to after a
// single sign-on. the sign-on is gone; the page says so and leads to the
// projects, where every dashboard now is.
export function SsoErrorPage() {
  return (
    <PublicFrame>
      <StopPage
        icon={<Link2Off aria-hidden />}
        title="This link no longer works"
        actions={<ProjectsButton />}
      >
        <p>The link is from an older version of the console.</p>
        <p>Your projects and their dashboards are in the console. Sign in to see them.</p>
      </StopPage>
    </PublicFrame>
  );
}
