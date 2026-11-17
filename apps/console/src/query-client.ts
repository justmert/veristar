import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";
import { ApiError } from "@/api/client";

// a 401 anywhere means the session is gone (expired, signed out in another
// tab, removed by the server). the browser goes to sign-in and comes back
// to the page it was on. two 401s keep the session: the operator area asks
// for a fresh passkey step-up (step_up_required), and a first passkey needs
// a newer sign-in (enrollment_closed). the step-up status is read again
// and the operator layout shows its prompt.
function onError(err: unknown) {
  if (
    err instanceof ApiError &&
    err.status === 401 &&
    (err.code === "step_up_required" || err.code === "enrollment_closed")
  ) {
    void queryClient.invalidateQueries({ queryKey: ["step-up"] });
    return;
  }
  if (err instanceof ApiError && err.status === 401 && window.location.pathname !== "/sign-in") {
    const back = window.location.pathname + window.location.search;
    window.location.assign(`/sign-in?return_to=${encodeURIComponent(back)}`);
  }
}

// how long to wait for the reconciler (18.9) between tries, and how many
// tries before the page stops and shows the message with a retry button
const PENDING_DELAY_MS = 10_000;
const PENDING_TRIES = 30;

function pending(err: unknown): err is ApiError {
  return err instanceof ApiError && err.code === "provisioning_pending";
}

// client errors do not get better by repeating them; network errors and 5xx
// get two more tries. provisioning_pending clears by itself once the
// reconciler has created the member's analytics access, so it is asked
// again for a few minutes.
export function retryQuery(count: number, err: unknown): boolean {
  if (pending(err)) return count < PENDING_TRIES;
  if (err instanceof ApiError && err.status < 500) return false;
  return count < 2;
}

export function retryDelay(count: number, err: unknown): number {
  if (pending(err)) return err.retryAfter ? err.retryAfter * 1000 : PENDING_DELAY_MS;
  // react query's default backoff
  return Math.min(1000 * 2 ** count, 30_000);
}

export const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError }),
  defaultOptions: {
    queries: {
      retry: retryQuery,
      retryDelay,
      refetchOnWindowFocus: true,
    },
    mutations: { retry: false },
  },
});
