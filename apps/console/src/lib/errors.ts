import { ApiError, ResponseShapeError } from "@/api/client";
import { PasskeyError } from "@/lib/passkey/errors";
import { WalletError } from "@/lib/stellar-wallet";

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
      case "sole_owner":
        return "You are the only owner of a project. Make another member an owner, or ask the operators to remove the project, before you delete your account.";
      case "last_owner":
        return "A project needs at least one owner. Make someone else an owner first.";
      case "site_removed":
        return "This site was deleted and can no longer be changed.";
      case "erasure_in_progress":
        return "Another erasure request for this site is still running. Wait for it to finish.";
      case "alert_source_unreachable":
        // the message names each alertmanager member and why it failed
        return `Alert source unreachable. ${err.message}.${ref}`;
      case "alert_source_not_configured":
        return "No Alertmanager is configured for the control plane (ALERTMANAGER_URLS), so no alerts can be shown.";
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
      case "passkey_unknown":
        return "No account signs in with this passkey. It was removed from its account, or the account was deleted. Create a new passkey to start a new account.";
      case "challenge_expired":
        return "The sign-in request expired. Start again.";
      case "challenge_used":
        return "This sign-in request was already used. Start again.";
      case "csrf_header_missing":
      case "origin_mismatch":
        return `The request was refused as cross-site (${err.code}).${ref}`;
      default:
        return `${err.message || "The request failed."} (${err.code}, HTTP ${err.status}).${ref}`;
    }
  }
  if (err instanceof ResponseShapeError) return err.message;
  if (err instanceof WalletError) {
    switch (err.code) {
      case "rejected":
        return "The wallet declined the request. Nothing was signed.";
      case "unavailable":
        return "The wallet is not installed or is locked. Unlock it and try again.";
      case "wrong_account":
        return "The wallet signed with another account. Switch to the account you chose and try again.";
      case "unsupported":
        return "This wallet cannot sign this kind of request. Choose another wallet.";
      default:
        return `The wallet reported an error: ${err.message}`;
    }
  }
  if (err instanceof PasskeyError) {
    switch (err.code) {
      case "cancelled":
        return "The passkey prompt was closed. Nothing was shared.";
      case "unsupported":
        return "This browser or device cannot make a passkey this console accepts (ES256 with a PIN or biometric check). Try another device or browser.";
      default:
        return `The passkey reported an error: ${err.message}`;
    }
  }
  if (err instanceof TypeError)
    return "The console could not reach the server. Check your connection.";
  return err instanceof Error ? err.message : "Something went wrong.";
}

export function isNotFound(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404;
}
