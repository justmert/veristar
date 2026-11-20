// the checks of a case as plain sentences, one icon each (failed, passed,
// not given, info). they help the operator judge whether a project is built
// on stellar; none of them decides it, so the words state what was found,
// never what the project meant.

import type { OperatorSite, OperatorTenantDetail, StellarToml } from "@/api/schemas";
import type { CheckItem } from "@/components/checks-list";
import { countOf, formatCount, formatDateTimeShort } from "@/lib/format";

const METHOD = { dns: "DNS TXT record", file: "File under /.well-known/" } as const;

function checked(at: string | null): string | null {
  return at ? `checked ${formatDateTimeShort(at)}` : null;
}

function joinDetail(...parts: (string | null | undefined)[]): string | null {
  const kept = parts.filter((p): p is string => Boolean(p));
  return kept.length ? kept.join(", ") : null;
}

export function domainCheck(s: OperatorSite, own = false): CheckItem {
  const name = own ? (
    "The domain"
  ) : (
    <>
      The domain of <b className="font-semibold">{s.domain}</b>
    </>
  );
  if (s.verification === "verified")
    return {
      key: `domain-${s.id}`,
      status: "passed",
      children: <>{name} is verified</>,
      detail: joinDetail(
        s.verify_method ? METHOD[s.verify_method as keyof typeof METHOD] : null,
        checked(s.verified_at ?? s.last_verify_at),
      ),
    };
  if (s.verification === "unverified")
    return {
      key: `domain-${s.id}`,
      status: "failed",
      children: <>{name} is not verified</>,
      detail: joinDetail(s.last_verify_error, checked(s.last_verify_at)),
    };
  return {
    key: `domain-${s.id}`,
    status: "missing",
    children: <>{name} is not checked yet</>,
  };
}

function tomlSummary(t: StellarToml): string {
  const parts: string[] = [];
  if (t.network) parts.push(t.network === "public" ? "public network" : `${t.network} network`);
  if (t.accounts_count !== null) parts.push(countOf(t.accounts_count, "account"));
  if (t.currencies_total !== null)
    parts.push(countOf(t.currencies_total, "currency", "currencies"));
  const keysValid =
    (t.accounts_count === null || t.accounts_valid === t.accounts_count) &&
    t.signing_key_valid !== false;
  parts.push(keysValid ? "keys valid" : "some keys not valid");
  return parts.join(", ");
}

export function tomlCheck(s: OperatorSite, own = false): CheckItem {
  const t = s.stellar_toml;
  const where = own ? "on this domain" : `on ${s.domain}`;
  const detail = checked(s.stellar_toml_checked_at);
  if (!t)
    return {
      key: `toml-${s.id}`,
      status: "missing",
      children: `stellar.toml not checked ${where}`,
    };
  if (t.status === "found")
    return {
      key: `toml-${s.id}`,
      status: t.signing_key_valid === false ? "warning" : "passed",
      children: `stellar.toml found ${where}: ${tomlSummary(t)}`,
      detail,
    };
  if (t.status === "not_found")
    return { key: `toml-${s.id}`, status: "missing", children: `No stellar.toml ${where}`, detail };
  return {
    key: `toml-${s.id}`,
    status: "warning",
    children: `stellar.toml ${where} could not be read`,
    detail: joinDetail(t.error ?? t.status.replace("_", " "), detail),
  };
}

export function originsCheck(s: OperatorSite): CheckItem {
  const own = s.origins.every((o) => {
    try {
      const host = new URL(o).hostname;
      return host === s.domain || host.endsWith(`.${s.domain}`);
    } catch {
      return false;
    }
  });
  return {
    key: `origins-${s.id}`,
    status: own ? "passed" : "info",
    children: own
      ? "Sends only from its own domain"
      : `Sends from ${countOf(s.origins.length, "origin")}`,
    detail: s.origins.length > 0 ? s.origins.map((o) => <span key={o}>{o}</span>) : null,
  };
}

// every check of a project's case
export function projectChecks(detail: OperatorTenantDetail): CheckItem[] {
  const t = detail.tenant;
  const items: CheckItem[] = [];
  if (detail.sites.length === 0)
    items.push({ key: "sites", status: "missing", children: "No site yet" });
  for (const s of detail.sites) {
    items.push(domainCheck(s));
    items.push(tomlCheck(s));
    if (s.domain_previously_removed)
      items.push({
        key: `prior-${s.id}`,
        status: "warning",
        children: (
          <>
            <b className="font-semibold">{s.domain}</b>, or a parent or subdomain of it, belonged to
            a removed project
          </>
        ),
      });
  }
  const ids = detail.stellar_ids;
  const invalid = ids.filter((i) => !i.valid).length;
  items.push(
    ids.length === 0
      ? { key: "ids", status: "missing", children: "No Stellar IDs given" }
      : {
          key: "ids",
          status: invalid > 0 ? "warning" : "passed",
          children:
            invalid > 0
              ? `${countOf(ids.length, "Stellar ID")} given, ${formatCount(invalid)} not a valid ID`
              : ids.length === 1
                ? "1 Stellar ID given, valid"
                : `${countOf(ids.length, "Stellar ID")} given, ${ids.length === 2 ? "both" : "all"} valid`,
          detail: (
            <>
              {ids.map((i) => (
                <span key={i.id}>
                  {i.id}, {i.valid ? (i.type ?? "").replace("_", " ") : "not a valid ID"}
                </span>
              ))}
            </>
          ),
        },
  );
  const scf = t.enrollment_answers.scf_round_url;
  items.push(
    scf
      ? { key: "scf", status: "info", children: "SCF round link given", detail: scf }
      : { key: "scf", status: "missing", children: "No SCF round link given" },
  );
  const received = Object.values(detail.ingest.last_30d).reduce((a, b) => a + b, 0);
  const accepted = detail.ingest.last_30d.accepted ?? 0;
  if (received > 0)
    items.push({
      key: "events",
      status: "info",
      children:
        t.status === "pending"
          ? `The sites already sent ${formatCount(received)} tracker requests in the last 30 full days; they are refused until approval`
          : `${formatCount(accepted)} of ${formatCount(received)} tracker requests accepted in the last 30 full days`,
    });
  else items.push({ key: "events", status: "missing", children: "No tracker requests yet" });
  return items;
}

// the checks of one site in review
export function siteChecks(s: OperatorSite): CheckItem[] {
  return [domainCheck(s, true), tomlCheck(s, true), originsCheck(s)];
}
