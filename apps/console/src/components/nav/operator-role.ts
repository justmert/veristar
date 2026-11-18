import { useQuery } from "@tanstack/react-query";
import { stepUpQuery } from "@/api/operators";
import { meQuery } from "@/api/queries";

// whether the signed-in operator is an operator admin, as /api/me names the
// roster role (4.1.4). undefined while it is not known yet; false outside the
// operator area and before the step-up, so nothing that needs the role shows
// early (the admin routes accept a stepped-up session only, 5.16).
export function useOperatorAdmin(enabled = true): boolean | undefined {
  const me = useQuery(meQuery);
  const admin = me.data?.user.operator_role === "operator_admin";
  const stepUp = useQuery({ ...stepUpQuery, enabled: enabled && admin });
  if (!me.data) return undefined;
  if (!enabled || !admin) return false;
  if (stepUp.isPending) return undefined;
  return stepUp.data?.stepped_up === true;
}
