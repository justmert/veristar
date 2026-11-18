import { cn } from "@veristar/brand/cn";
import { CircleAlert, CircleCheck, CircleDashed, CircleX } from "lucide-react";
import type { ReactNode } from "react";
import type { StellarToml, Verification, VerifyMethod } from "@/api/schemas";
import { Pill, type Tone } from "@/components/status";
import { formatDate, formatDateTimeUtc } from "@/lib/format";

// domain verification (contract 5.8) and the stellar.toml signal (5.12),
// shown the same way to members on their sites and to operators in review.
// both are evidence only: neither decides anything on its own.

const VERIFY_TONE: Record<Verification, Tone> = {
  verified: "success",
  unverified: "warning",
  unchecked: "info",
};

const VERIFY_TEXT: Record<Verification, string> = {
  verified: "Domain verified",
  unverified: "Domain not verified",
  unchecked: "Domain not checked yet",
};

export function VerificationPill({
  verification,
  live,
}: {
  verification: Verification;
  live?: boolean;
}) {
  return (
    <Pill tone={VERIFY_TONE[verification]} live={live}>
      {VERIFY_TEXT[verification]}
    </Pill>
  );
}

// the last_verify_error codes of contract 5.8
const VERIFY_ERRORS: Record<string, string> = {
  dns_no_record: "No TXT record was found at the expected name.",
  dns_mismatch: "A TXT record exists, but its value does not match.",
  dns_error: "The DNS lookup failed.",
  file_not_found: "The verification file was not found (the server did not answer 200).",
  file_mismatch: "The verification file exists, but its content does not match.",
  file_too_large: "The verification file is larger than 1,024 bytes.",
  refused_address: "The domain resolves to an address the checker does not contact.",
  redirect_refused: "The file request was redirected to another host, which is not followed.",
  tls_error: "The HTTPS connection to the domain failed.",
  timeout: "The domain did not answer in time.",
  fetch_error: "The file could not be fetched.",
};

export function verifyErrorText(code: string): string {
  return VERIFY_ERRORS[code] ?? `The check failed (${code}).`;
}

// what a person can do about each failure, in the words of the record table
const VERIFY_ADVICE: Record<string, string> = {
  dns_no_record:
    "Add the record at the name shown. DNS changes can take a few minutes to appear, sometimes longer.",
  dns_mismatch: "Check that the value is copied whole, without quotes or extra spaces.",
  dns_error: "The domain's DNS servers did not answer the lookup. Try again in a few minutes.",
  file_not_found: "Upload the file at exactly the address shown, so it is served with status 200.",
  file_mismatch: "The file must contain only the content shown, with no other text.",
  file_too_large: "The file must contain only the content shown, with no other text.",
  refused_address:
    "The checker only contacts public addresses. The DNS record works for a site that is not public.",
  redirect_refused: "Serve the file from the domain itself, without a redirect to another host.",
  tls_error:
    "The file check needs a valid HTTPS certificate on the domain. The DNS record works without one.",
  timeout: "The domain did not answer within 15 seconds. Try again, or use the other method.",
  fetch_error: "Try again in a few minutes, or use the other method.",
};

export function verifyAdvice(code: string): string | null {
  return VERIFY_ADVICE[code] ?? null;
}

const METHOD_TEXT: Record<VerifyMethod, string> = { dns: "DNS TXT record", file: "file" };

export function VerificationDetail({
  verification,
  verifyMethod,
  verifiedAt,
  lastVerifyAt,
  lastVerifyError,
}: {
  verification: Verification;
  verifyMethod: VerifyMethod | null;
  verifiedAt: string | null;
  lastVerifyAt: string | null;
  lastVerifyError: string | null;
}) {
  return (
    <div className="flex flex-col items-start gap-1 text-small">
      <VerificationPill verification={verification} />
      {verifiedAt && verifyMethod ? (
        <p className="text-fg-2">
          Last verified {formatDate(verifiedAt)} by {METHOD_TEXT[verifyMethod]}.
        </p>
      ) : null}
      {lastVerifyAt ? <p className="text-fg-2">Last checked {formatDate(lastVerifyAt)}.</p> : null}
      {lastVerifyError ? (
        <p className="flex items-start gap-1 text-fg">
          <CircleAlert aria-hidden className="mt-1 size-3.5 shrink-0 text-warning" />
          {verifyErrorText(lastVerifyError)}
        </p>
      ) : null}
    </div>
  );
}

const TOML_STATUS: Record<StellarToml["status"], string> = {
  found: "Found",
  not_found: "Not found",
  invalid: "Not a valid stellar.toml",
  too_large: "Larger than 100 KB",
  refused: "Not fetched (address or redirect refused)",
  error: "Could not be fetched",
};

const NETWORK_TEXT: Record<NonNullable<StellarToml["network"]>, string> = {
  public: "Public network",
  testnet: "Testnet",
  futurenet: "Futurenet",
  other: "Another network",
};

// one state per checklist row, the same vocabulary as the evidence list:
// ok (proven), partial (part of it, or a risk), missing (given, and the
// check failed), absent (nothing given to check). each is an icon, a tone
// and a word for screen readers; the row's text says the state too
type Mark = "ok" | "partial" | "missing" | "absent";

const MARK_ICON: Record<Mark, ReactNode> = {
  ok: <CircleCheck aria-hidden className="size-3.5 text-success" strokeWidth={2.4} />,
  partial: <CircleAlert aria-hidden className="size-3.5 text-warning" strokeWidth={2.4} />,
  missing: <CircleX aria-hidden className="size-3.5 text-critical" strokeWidth={2.4} />,
  absent: <CircleDashed aria-hidden className="size-3.5 text-fg-3" strokeWidth={2.4} />,
};

const MARK_WORD: Record<Mark, string> = {
  ok: "Passed",
  partial: "Partly passed",
  missing: "Failed",
  absent: "Not given",
};

// what to do about a stellar.toml check that did not pass, in one sentence
const TOML_ADVICE: Record<Exclude<StellarToml["status"], "found">, string> = {
  not_found: "Serve the file at this address with status 200.",
  invalid: "Fix the TOML syntax of the file. A TOML validator shows the line.",
  too_large: "Make the file smaller than 100 KB.",
  refused: "Serve the file from the domain itself on a public address, without a redirect.",
  error: "Make sure the domain answers over HTTPS. The next daily check reads it again.",
};

export function tomlAdvice(status: StellarToml["status"]): string | null {
  return status === "found" ? null : TOML_ADVICE[status];
}

// one checklist row; the icon repeats what the words already say. a row that
// did not pass says what to do next.
function EvidenceRow({
  mark,
  label,
  children,
  advice,
}: {
  mark: Mark;
  label: string;
  children: ReactNode;
  advice?: string | null;
}) {
  return (
    <div className="grid grid-cols-[1rem_minmax(0,8rem)_minmax(0,1fr)] items-start gap-x-2 py-1">
      <span className="mt-1 flex" data-mark={mark}>
        {MARK_ICON[mark]}
        <span className="sr-only">{MARK_WORD[mark]}: </span>
      </span>
      <dt className="text-fg-2">{label}</dt>
      <dd className="min-w-0 break-words text-fg">
        {children}
        {(mark === "partial" || mark === "missing") && advice ? (
          <span className="block text-fg-2" data-advice="">
            {advice}
          </span>
        ) : null}
      </dd>
    </div>
  );
}

// stellar.toml is fetched by the service, so it reads as evidence rows with
// no button: each row says what was found
export function StellarTomlSummary({
  toml,
  checkedAt,
  domain,
  className,
}: {
  toml: StellarToml | null;
  checkedAt: string | null;
  domain: string;
  className?: string;
}) {
  const where = `https://${domain}/.well-known/stellar.toml`;
  const head = (
    <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
      <span className="text-body font-medium text-fg">stellar.toml</span>
      <span className="text-caption font-normal text-fg-3">
        {checkedAt
          ? `Checked after setup and daily, last ${formatDateTimeUtc(checkedAt)}`
          : "Checked after setup and daily"}
      </span>
    </div>
  );
  if (!toml) {
    return (
      <div className={cn("flex flex-col gap-1 text-small", className)}>
        {head}
        <p className="text-fg-2">
          Not checked yet. It is fetched from <span className="break-all font-mono">{where}</span>{" "}
          shortly after the site is added and once a day.
        </p>
      </div>
    );
  }
  const found = toml.status === "found";
  const currencies = toml.currencies ?? [];
  const invalidCurrencies = currencies.filter((c) => !c.valid).length;
  const accounts = toml.accounts_count ?? 0;
  const total = toml.currencies_total ?? 0;
  return (
    <div className={cn("flex flex-col gap-1 text-small", className)}>
      {head}
      <dl className="flex flex-col divide-y divide-line-subtle">
        <EvidenceRow mark={found ? "ok" : "missing"} label="File" advice={tomlAdvice(toml.status)}>
          {TOML_STATUS[toml.status]}
          {toml.http_status !== null && !found ? ` (HTTP ${toml.http_status})` : ""}
          {found ? null : <span className="block break-all font-mono text-fg-3">{where}</span>}
        </EvidenceRow>
        {found ? (
          <>
            <EvidenceRow mark={toml.network ? "ok" : "absent"} label="Network">
              {toml.network ? NETWORK_TEXT[toml.network] : "Not given"}
            </EvidenceRow>
            <EvidenceRow mark={toml.version ? "ok" : "absent"} label="Version">
              {toml.version ?? "Not given"}
            </EvidenceRow>
            <EvidenceRow
              mark={
                accounts === 0
                  ? "absent"
                  : (toml.accounts_valid ?? 0) === 0
                    ? "missing"
                    : (toml.accounts_valid ?? 0) < accounts
                      ? "partial"
                      : "ok"
              }
              label="Accounts"
              advice="List each account in ACCOUNTS as a valid G address."
            >
              {accounts} listed, {toml.accounts_valid ?? 0} valid
            </EvidenceRow>
            <EvidenceRow
              mark={toml.signing_key_present ? (toml.signing_key_valid ? "ok" : "missing") : "absent"}
              label="Signing key"
              advice="Set SIGNING_KEY to a valid G address."
            >
              {toml.signing_key_present
                ? toml.signing_key_valid
                  ? "Present and valid"
                  : "Present, not a valid key"
                : "Not given"}
            </EvidenceRow>
            <EvidenceRow
              mark={toml.org_url ? (toml.org_url_same_domain ? "ok" : "partial") : "absent"}
              label="Organization URL"
              advice="Set ORG_URL to an address on this domain."
            >
              <span className="break-all">{toml.org_url ?? "Not given"}</span>
              {toml.org_url
                ? toml.org_url_same_domain
                  ? " (same domain)"
                  : " (another domain)"
                : ""}
            </EvidenceRow>
            <EvidenceRow
              mark={
                total === 0
                  ? "absent"
                  : invalidCurrencies >= total
                    ? "missing"
                    : invalidCurrencies > 0
                      ? "partial"
                      : "ok"
              }
              label="Currencies"
              advice="Give each currency a valid issuer or contract."
            >
              {total}
              {total > currencies.length ? `, first ${currencies.length} shown` : ""}
              {invalidCurrencies > 0 ? `, ${invalidCurrencies} not valid` : ""}
            </EvidenceRow>
          </>
        ) : null}
      </dl>
      {found && currencies.length > 0 ? (
        <ul aria-label="Currencies in stellar.toml" className="flex flex-col gap-1 pl-6">
          {currencies.map((c, i) => (
            <li
              // codes repeat across issuers, so the position keeps keys unique;
              // the list never reorders
              key={`${i}-${c.code}`}
              className="flex flex-wrap items-baseline gap-x-2 text-caption font-normal"
            >
              <span className="font-medium text-fg">{c.code}</span>
              <span className="min-w-0 break-all font-mono text-fg-3">
                {c.issuer ?? c.contract ?? "no issuer or contract"}
              </span>
              {c.valid ? null : <span className="text-warning">not valid</span>}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
