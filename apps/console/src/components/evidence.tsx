import type { StellarToml, Verification, VerifyMethod } from "@/api/schemas";
import { formatDate } from "@/lib/format";

// domain verification (contract 5.8) and the stellar.toml signal (5.12),
// shown the same way to members on their sites and to operators in review.
// both are evidence only: neither decides anything on its own.

const VERIFY_TONE: Record<Verification, string> = {
  verified: "bg-good/12 text-good ring-good/30",
  unverified: "bg-warn/12 text-warn ring-warn/30",
  unchecked: "bg-muted text-muted-foreground ring-border",
};

const VERIFY_TEXT: Record<Verification, string> = {
  verified: "Domain verified",
  unverified: "Domain not verified",
  unchecked: "Domain not checked yet",
};

export function VerificationPill({ verification }: { verification: Verification }) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${VERIFY_TONE[verification]}`}
    >
      <span aria-hidden className="size-1.5 rounded-full bg-current" />
      {VERIFY_TEXT[verification]}
    </span>
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
    <div className="space-y-1 text-sm">
      <VerificationPill verification={verification} />
      {verifiedAt && verifyMethod ? (
        <p className="text-muted-foreground">
          Last verified {formatDate(verifiedAt)} by {METHOD_TEXT[verifyMethod]}.
        </p>
      ) : null}
      {lastVerifyAt ? (
        <p className="text-muted-foreground">Last checked {formatDate(lastVerifyAt)}.</p>
      ) : null}
      {lastVerifyError ? <p className="text-warn">{verifyErrorText(lastVerifyError)}</p> : null}
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

export function StellarTomlSummary({
  toml,
  checkedAt,
  domain,
}: {
  toml: StellarToml | null;
  checkedAt: string | null;
  domain: string;
}) {
  const where = `https://${domain}/.well-known/stellar.toml`;
  if (!toml) {
    return (
      <p className="text-sm text-muted-foreground">
        stellar.toml not checked yet. It is fetched from {where} shortly after the site is added and
        once a day.
      </p>
    );
  }
  const found = toml.status === "found";
  const currencies = toml.currencies ?? [];
  return (
    <div className="space-y-2 text-sm">
      <p>
        <span className="font-medium">stellar.toml: </span>
        {TOML_STATUS[toml.status]}
        {toml.http_status !== null && !found ? ` (HTTP ${toml.http_status})` : ""}
        {toml.error ? ` (${toml.error})` : ""}
        {checkedAt ? (
          <span className="text-muted-foreground">, checked {formatDate(checkedAt)}</span>
        ) : null}
      </p>
      {found ? (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
          <dt className="text-muted-foreground">Network</dt>
          <dd>{toml.network ? NETWORK_TEXT[toml.network] : "Not stated"}</dd>
          <dt className="text-muted-foreground">Version</dt>
          <dd>{toml.version ?? "Not stated"}</dd>
          <dt className="text-muted-foreground">Accounts</dt>
          <dd>
            {toml.accounts_count ?? 0} listed, {toml.accounts_valid ?? 0} valid
          </dd>
          <dt className="text-muted-foreground">Signing key</dt>
          <dd>
            {toml.signing_key_present
              ? toml.signing_key_valid
                ? "Present and valid"
                : "Present, not a valid key"
              : "None"}
          </dd>
          <dt className="text-muted-foreground">Organization URL</dt>
          <dd className="break-all">
            {toml.org_url ?? "None"}
            {toml.org_url
              ? toml.org_url_same_domain
                ? " (same domain)"
                : " (another domain)"
              : ""}
          </dd>
          <dt className="text-muted-foreground">Currencies</dt>
          <dd>
            {toml.currencies_total ?? 0}
            {(toml.currencies_total ?? 0) > currencies.length
              ? `, first ${currencies.length} shown`
              : ""}
          </dd>
        </dl>
      ) : null}
      {found && currencies.length > 0 ? (
        <ul className="space-y-1" aria-label="Currencies in stellar.toml">
          {currencies.map((c, i) => (
            <li
              // codes repeat across issuers, so the position keeps keys unique;
              // the list never reorders
              key={`${i}-${c.code}`}
              className="flex flex-wrap items-baseline gap-x-2 font-mono text-xs"
            >
              <span className="font-sans font-medium">{c.code}</span>
              <span className="break-all text-muted-foreground">
                {c.issuer ?? c.contract ?? "no issuer or contract"}
              </span>
              {c.valid ? null : <span className="font-sans text-warn">not valid</span>}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
