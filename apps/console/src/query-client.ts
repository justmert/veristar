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

export const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError }),
  defaultOptions: {
    queries: {
      // client errors do not get better by repeating them; network errors
      // and 5xx get two more tries
      retry: (count, err) => {
        if (err instanceof ApiError && err.status < 500) return false;
        return count < 2;
      },
      refetchOnWindowFocus: true,
    },
    mutations: { retry: false },
  },
});
