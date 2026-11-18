import { useQuery } from "@tanstack/react-query";
import { rosterQuery, stepUpQuery } from "@/api/operators";
import { meQuery } from "@/api/queries";

// whether the signed-in operator is an operator admin. the session does not
// carry the operator role, so it is read from the roster, which every
// operator may read once the session holds a step-up (contract 5.16).
// undefined while it is not known yet; false outside the operator area
// before the step-up, so nothing that needs the role shows early.
export function useOperatorAdmin(enabled = true): boolean | undefined {
  const me = useQuery(meQuery);
  const operator = me.data?.user.is_operator === true;
  const stepUp = useQuery({ ...stepUpQuery, enabled: enabled && operator });
  const live = enabled && operator && stepUp.data?.stepped_up === true;
  const roster = useQuery({ ...rosterQuery, enabled: live });
  if (!me.data) return undefined;
  if (!operator) return false;
  if (!enabled) return false;
  if (stepUp.isPending) return undefined;
  if (!live) return false;
  if (roster.error) return false;
  if (!roster.data) return undefined;
  const u = me.data.user;
  return roster.data.items.some(
    (e) =>
      e.status === "active" &&
      e.role === "operator_admin" &&
      (e.account?.user_id === u.id ||
        (e.github_id !== null && e.github_id === u.github_id) ||
        (e.stellar_address !== null && e.stellar_address === u.stellar_address)),
  );
}
