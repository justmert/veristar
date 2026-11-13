# Jurisdictions: use without and with end-user opt-in

> **Status: draft, pending legal review an operator input.** Not legal advice. Last
> updated 2026-11-13. This is the explicit jurisdictions statement the RFP asks
> for. Every row cites `sources.md`; a row without a confirmed source says so.

## Why this needs a per-country answer

The tracker runs JavaScript that makes the browser send information (page
path, referrer, title, screen, language) and reads one browser storage key
(the opt-out flag). The EDPB says that a script instructing the browser to send
information "clearly falls within the scope" of Art. 5(3) of the ePrivacy
Directive (S1, para 33), and that whether an exemption applies is for the
national rules (S1, para 56). Cookieless measurement is therefore not exempt
from consent by itself; it depends on each country's exemption.

The service gives tenants two modes:

- **Default mode:** the tracker starts on page load. It honours Global Privacy
  Control, the opt-out flag and, when enabled, Do Not Track
  (`privacy-strategy.md` section 6).
- **Consent mode:** `data-auto-track="false"`; the tracker sends nothing and
  reads nothing until the site calls `umami.start()` after the visitor agrees
  (`docs/reference/wire-v1.md`).

## Summary table

"Default mode possible" means the source supports measurement without prior
consent under conditions, and the service as built meets the conditions we
could check. It is never a guarantee: each tenant is the controller and decides
for its own site.

| Jurisdiction | Default mode possible | Conditions and gaps | Source, confidence |
|---|---|---|---|
| France | Not as built | CNIL exempts audience measurement used only for the publisher's own statistics, producing anonymous statistics, without cross-site tracking or combination with other processing (délibération 2020-091, art. 5). Its July 2025 self-assessment tool names technical measures that are recommendations an alternative may replace (S2). The referrer is now limited to the host as recommended. Still not met: reports are exact counts, not rounded to ten, and no anonymity analysis replaces the rounding; Umami's Sessions page shows one visitor's pages of a day; UTM parameters are kept although the tool recommends not collecting them; and no reuse for the provider's account (the SCF operator view is an open question). See `privacy-strategy.md` sections 8 and 9. Use consent mode until these are resolved or counsel decides otherwise. | S2, high |
| United Kingdom | Yes, with conditions | PECR Sch. A1 para 5 (inserted by the Data (Use and Access) Act 2025, in force 5 February 2026): sole purpose of statistics to improve the service, not shared except with a party that helps improve it, clear and comprehensive information, and a simple free means to object. The ICO says a third-party analytics provider must be a processor, and objection must not rely on browser settings alone. Tenants must state the processing in their privacy notice and offer an opt-out link that sets the opt-out flag. | S3, high |
| Germany | No (use consent mode) | § 25 TDDDG has no analytics exemption. The supervisory authorities (DSK, OH Digitale Dienste v1.2) make no general finding that reach measurement is strictly necessary; it is case by case. | S4, high |
| Netherlands | Possibly, with conditions | Art. 11.7a(3)(b) Telecommunicatiewet exempts access with no or little privacy impact to learn about the quality or effectiveness of the service. The AP's page describes limited analytics cookies without consent. We found no AP guidance on cookieless techniques; the reading that the exemption covers them rests on the statute's wording. | S5, high on statute, medium on practice |
| Italy | Possibly, with conditions | Garante guidelines of 10 June 2021, section 7.2: analytics treated like technical cookies when they cannot identify the person directly, serve only aggregate statistics, cover a single site, are not combined by third parties, and (for third-party tools) mask at least the last IPv4 octet. The service stores no IP address. The exact wording must be rechecked against the full text. | S6, medium |
| Other EU and EEA states | Not assessed | No primary source was checked. Use consent mode. | none |
| Turkey | Possibly, with conditions (contradicts report.md) | The current KVKK cookie guide (July 2025 edition), section 5.9, places first-party analytics under "Kriter B", outside explicit consent, when used only for anonymous statistics and not for cross-site tracking. The service runs as a third party on the tenant's behalf; the guide does not address third-party analytics as such. Treat as unresolved for counsel. | S7, high on the text, open on its application |
| United States (CCPA/CPRA) | Not confirmed | The primary source could not be loaded. | S11, low |
| Brazil (LGPD) | Not confirmed | The primary source could not be loaded. | S11, low |

## What tenants must do in every jurisdiction

- Name the service and the processing in the site's privacy notice.
- Offer a simple opt-out link that sets the opt-out flag
  (`localStorage.setItem('umami.disabled', '1')`).
- Put no personal data in URLs, page titles, referrers or event data (`terms.md`).
- Where in doubt, use consent mode.

## Pending EU change

The Commission's Digital Omnibus proposal COM(2025) 837 would add a GDPR Art.
88a allowing storage or access without consent for aggregated audience
measurement by the controller of an online service for its own use. It is a
proposal; its status on 2026-11-12 was not checked (S4).
