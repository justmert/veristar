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
        return "No Alertmanager is configured for this deployment, so no alerts can be shown. Add one in the deploy settings.";
      case "operator_required":
        return "Only operators of the service can do this.";
      case "operator_admin_required":
        return "Only operator admins can change the roster.";
      case "step_up_required":
        return "Confirm with your passkey or security key to continue.";
      case "step_up_failed":
        return "The passkey answer could not be verified. Try again with a passkey registered to this account.";
      case "ceremony_expired":
        return "The passkey prompt expired. Start again.";
      case "passkey_required":
        return "Register a passkey or security key first.";
      case "last_passkey":
        return "Register another passkey before you delete your last one.";
      case "enrollment_closed":
        return "Sign in again to register your first passkey.";
      case "last_operator_admin":
        return "The roster needs at least one active operator admin. Add or promote another admin first.";
      case "bootstrap_operator":
        return "This operator is listed in the deploy settings. Take it out of the settings first.";
      case "operator_exists":
        return "This identity is on the roster already.";
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
      case "period_not_final":
        return "End the report at least four days before today (UTC). The most recent days are still being counted and could change after signing.";
      case "rate_limited":
        return `Too many requests. Try again in ${waitText(err.retryAfter)}.`;
      case "unavailable":
        return `The service does not answer right now. Try again in ${waitText(err.retryAfter)}.${ref}`;
      case "provisioning_pending":
        // 18.9: the member's analytics access is still being created; the
        // query client asks again on its own (query-client.ts)
        return "Your access to this project's web analytics is being set up. This page loads it on its own once it is ready.";
      case "passkey_unknown":
        return "No account signs in with this passkey. It was removed from its account, or the account was deleted. Create a new passkey to start a new account.";
      case "challenge_expired":
        return "The sign-in request expired. Start again.";
      case "challenge_used":
        return "This sign-in request was already used. Start again.";
      case "challenge_invalid":
        return "The signature does not match this request. Start again, and sign with the account or passkey you chose.";
      case "identity_in_use":
        return "That sign-in method belongs to another account. Remove it there first, or sign in with it instead.";
      case "identity_already_linked":
        return "Your account has a sign-in method of this kind already. Unlink it first to link another one.";
      case "last_sign_in_method":
        return "This is the only way to sign in to your account. Link another method before you unlink it.";
      case "too_many_keys":
        return "You have 20 active access keys, the most allowed. Revoke one before you create another.";
      case "not_reportable":
        return "The operator views do not list this project, so it cannot sign a report.";
      case "no_proposal":
        return "There is no proposal to confirm or dismiss. Reload to see the current values.";
      case "rule_limit":
        return "The service holds 200 rules, the most allowed. Delete one before you add another.";
      case "invalid_query":
        return `The request was refused: ${err.message || "a filter or range is not valid"}. Check the filters and the range.${ref}`;
      case "csrf_header_missing":
      case "origin_mismatch":
        return `The request was refused as cross-site (${err.code}).${ref}`;
      default:
        return `${err.message || "The request failed."} (${err.code}, HTTP ${err.status}).${ref}`;
    }
  }
  if (err instanceof ResponseShapeError)
    return "The server answered in a form this console does not read. Reload the page; if this repeats, report it to the operators.";
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

// a wait named by Retry-After, or a minute without one
function waitText(seconds: number | null): string {
  if (seconds === null || seconds <= 0) return "a minute";
  if (seconds < 60) return seconds === 1 ? "1 second" : `${seconds} seconds`;
  const m = Math.ceil(seconds / 60);
  return m === 1 ? "1 minute" : `${m} minutes`;
}

export function isNotFound(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404;
}

const UUID = /\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi;
const PATH = /(^|\s)\/[\w./{}:%?&=,+-]+/g;

// the error as one sentence for a person: errorText without the transport
// detail (reference, code, status, ids and paths). the detail goes in
// errorDetails, which "Copy details" puts on the clipboard.
export function errorMessage(err: unknown): string {
  // an answer without the error envelope (a proxy, a crash) has no sentence
  // for a person; its status and path go only in the copied details
  if (err instanceof ApiError && err.code === "unexpected_response")
    return "The service answered with an error. Try again.";
  const text = errorText(err)
    .replace(/\s*Reference \S+?\.(?=\s|$)/g, "")
    .replace(/\s*\([a-z_]+, HTTP \d+\)/g, "")
    .replace(/\s*\((?:csrf_header_missing|origin_mismatch)\)/g, "")
    .replace(UUID, "")
    .replace(PATH, "$1")
    .replace(/\s{2,}/g, " ")
    .replace(/\s+([.,])/g, "$1")
    .replace(/\.{2,}/g, ".")
    .trim();
  return text && text !== "." ? text : "The request failed.";
}

// what an operator needs to trace a failure in the logs, as plain lines
export function errorDetails(err: unknown, now: Date = new Date()): string {
  const lines: string[] = [];
  if (err instanceof ApiError) {
    if (err.requestId) lines.push(`Request id: ${err.requestId}`);
    lines.push(`Error code: ${err.code}`, `HTTP status: ${err.status}`);
    if (err.message) lines.push(`Message: ${err.message}`);
  } else if (err instanceof Error) {
    lines.push(`Error: ${err.name}`, `Message: ${err.message}`);
  } else if (err !== undefined && err !== null) {
    lines.push(`Error: ${String(err)}`);
  }
  lines.push(`Time: ${now.toISOString()}`);
  return lines.join("\n");
}
