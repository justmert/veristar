import { SignedReports } from "@/pages/reports";
import { SCFViewPage } from "@/pages/scf-view";

// what SCF sees about the project and the signed reports of the same
// figures, on one page: the report is that view in a form to share
export function ScfReviewPage() {
  return (
    <div className="flex flex-col gap-8">
      <SCFViewPage />
      <SignedReports />
    </div>
  );
}
