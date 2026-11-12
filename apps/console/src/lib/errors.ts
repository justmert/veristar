import { ApiError, ResponseShapeError } from "@/api/client";

// people-facing text for an error thrown by the api client. codes come from
// contract 2.4; the request id is shown so a report can be traced in the logs.
export function errorText(err: unknown): string {
  if (err instanceof ApiError) {
    const ref = err.requestId ? ` Reference ${err.requestId}.` : "";
    switch (err.code) {
      case "unauthenticated":
        return "Your session ended. Sign in again.";
      case "not_found":
        return "Not found, or you are not a member of this project.";
      case "owner_required":
        return "Only owners of this project can do this.";
      case "terms_not_accepted":
        return "An owner must accept the current terms before anything can be changed.";
      case "terms_version_mismatch":
        return "The terms changed while you were reading them. Reload and review the new version.";
      case "slug_taken":
        return "That project address is taken. Choose another one.";
      case "already_member":
        return "That person is already a member.";
      case "invite_exists":
        return "That person already has an open invite.";
      case "last_owner":
        return "A project needs at least one owner. Make someone else an owner first.";
      case "site_removed":
        return "This site was deleted and can no longer be changed.";
      case "operator_required":
        return "Only operators of the service can do this.";
      case "invalid_transition":
        // the message names the current status (contract 5.5), so it is shown
        return `This is not possible from the current status. ${err.message}`;
      case "tenant_suspended":
        return "This project is suspended. Sites and tokens cannot be added or changed until an operator resumes it.";
      case "tenant_not_active":
        return "This project is not active yet. Tokens can be created once operators approve it.";
      case "tracker_version_revoked":
        return "This tracker version was revoked. Choose another version.";
      case "validation_failed":
        return "Some fields need attention.";
      case "rate_limited":
        return `Too many requests. Try again${err.retryAfter ? ` in ${err.retryAfter} s` : " shortly"}.`;
      case "unavailable":
      case "provisioning_pending":
        return `The service is temporarily unavailable. Try again${err.retryAfter ? ` in ${err.retryAfter} s` : " shortly"}.${ref}`;
      case "csrf_header_missing":
      case "origin_mismatch":
        return `The request was refused as cross-site (${err.code}).${ref}`;
      default:
        return `${err.message || "The request failed."} (${err.code}, HTTP ${err.status}).${ref}`;
    }
  }
  if (err instanceof ResponseShapeError) return err.message;
  if (err instanceof TypeError)
    return "The console could not reach the server. Check your connection.";
  return err instanceof Error ? err.message : "Something went wrong.";
}

export function isNotFound(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404;
}
