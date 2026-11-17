---
title: "Jurisdictions: use without and with end-user opt-in"
sidebarTitle: "Jurisdictions"
description: "Where the tracker can run without end-user opt-in, and where consent mode applies."
---

Where a tenant can run the tracker without asking visitors for consent first,
and where consent mode applies. Every row cites the legal sources register
(`sources.md`); a row without a confirmed source says so. This page is not
legal advice. Last updated 2026-11-17.

## Why this needs a per-country answer

The tracker runs JavaScript that makes the browser send information and reads
one browser storage key (the opt-out flag). Tracker 1.3.0 sends the page path,
the referrer and the hostname, and the page title only when the site sets
`data-send-title="true"`; it reads whether the page runs inside a frame and
the page's visibility and prerendering state, which decide when the page view
is sent, and never reads the screen size or the browser language. Releases
before 1.3.0 also send the page title, the screen size and the browser
language. That is all the default tracker reads. On a site that opts
in to Wallets Kit capture with the script tag (`data-wallets-kit="true"`), it
also lists the names of the site's `localStorage` keys twice a second and reads
the kit's selected wallet id (never the address), from the moment tracking
starts and never for a visitor with Global Privacy Control, the opt-out flag or
an honoured Do Not Track, even when the site calls `umami.start()` for such a
visitor (tracker 1.2.0; 1.1.0 started the watcher on `umami.start()` without
that check). The exact list and timing is in `privacy-strategy.md` section 6.
That reading is access to stored information in its own right under Art. 5(3)
of the ePrivacy Directive, so this table covers the default tracker only, and a
tenant that turns Wallets Kit capture on must add it to its own assessment:
where a row below says no opt-in is needed, that holds for the default tracker,
and the Wallets Kit read needs its own exemption or the visitor's consent
(consent mode below). The EDPB says that a script instructing the browser to send
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
consent under conditions, and the service as built meets the conditions that
were checked. It is never a guarantee: each tenant is the controller and
decides for its own site.

| Jurisdiction | Default mode possible | Conditions and gaps | Source, confidence |
|---|---|---|---|
| France | Not as built | CNIL exempts audience measurement used only for the publisher's own statistics, producing anonymous statistics, without cross-site tracking or combination with other processing (délibération 2020-091, art. 5). Its July 2025 self-assessment tool names technical measures that are recommendations an alternative may replace (S2). Met: the referrer is limited to the host, and UTM parameters are dropped by default, both as the tool recommends (a site that turns on its `collect_utm` setting keeps UTM parameters and does not meet that measure). Not met: reports are exact counts, not rounded to ten, and no anonymity analysis replaces the rounding, and the session lists of the console's session finder and the MCP tools show single sessions, the latter with one visitor's pages of a day; and the operators' cross-tenant view reads aggregates that are not rounded, so the "no reuse for the provider's own account" condition depends on whether they count as anonymous. See `privacy-strategy.md` sections 1, 8 and 9. Use consent mode for visitors in France unless your counsel decides otherwise. | S2, high |
| United Kingdom | Yes, with conditions | PECR Sch. A1 para 5 (inserted by the Data (Use and Access) Act 2025, in force 5 February 2026): sole purpose of statistics to improve the service, not shared except with a party that helps improve it, clear and comprehensive information, and a simple free means to object. The ICO says a third-party analytics provider must be a processor, and objection must not rely on browser settings alone. Tenants must state the processing in their privacy notice and offer an opt-out link that sets the opt-out flag. | S3, high |
| Germany | No (use consent mode) | § 25 TDDDG has no analytics exemption. The supervisory authorities (DSK, OH Digitale Dienste v1.2) make no general finding that reach measurement is strictly necessary; it is case by case. | S4, high |
| Netherlands | Possibly, with conditions | Art. 11.7a(3)(b) Telecommunicatiewet exempts access with no or little privacy impact to learn about the quality or effectiveness of the service. The AP's page describes limited analytics cookies without consent. No AP guidance on cookieless techniques was found; the reading that the exemption covers them rests on the statute's wording. | S5, high on statute, medium on practice |
| Italy | Possibly, with conditions | Garante guidelines of 10 June 2021, section 7.2: analytics treated like technical cookies when they cannot identify the person directly, serve only aggregate statistics, cover a single site, are not combined by third parties, and (for third-party tools) mask at least the last IPv4 octet. The service stores no IP address. The exact wording must be rechecked against the full text. | S6, medium |
| Spain | Possibly, with conditions | The AEPD exempts audience measurement under LSSI art. 22.2 when it is only for the publisher's own audience, produces anonymous statistics only, uses no identifier across sites, keeps each publisher's data separate, runs under an Art. 28 contract with no reuse, and collects only the listed measurements (language is not listed). Tracker lifetime about 13 months, data kept at most 25 months (the raw retention default is 25 months). The same open points as France apply: exact counts and the session lists. | S39, high |
| Liechtenstein | Possibly, with conditions | The Datenschutzstelle accepts simple analytics without consent: only the controller's own visitor measurement, anonymised statistics for one site, no linking or passing on, no cross-site tracking, information to users, limited storage and a right to object. A processor must keep customers separate and use no identifier attributable to one person. The daily visitor hash must be checked against that last condition. | S29, high |
| Luxembourg | Possibly, with conditions | Consent is the default. The CNPD exempts analytics only where the site operator shows they are needed to provide the service (for example server capacity or navigation problems), with no sharing, no combination, no cross-site tracking, for the publisher only, and anonymous statistics only. | S31, medium |
| Finland | Possibly, with conditions | Traficom says analytics cannot plainly be called necessary. A provider that treats them as strictly necessary must show clear grounds, share nothing with third parties and keep individual visitors unidentifiable; otherwise it should ask for consent. | S23, medium |
| Bulgaria | Possibly, with conditions | ZET art. 4a asks for GDPR Art. 13 information and a way to refuse (opt-out wording). The CPDP says statistics need consent unless used in anonymised form and not shared, and elsewhere describes the rule as prior consent. Raw events with a daily visitor hash are not anonymised. Use consent mode unless your counsel decides otherwise. | S17, medium |
| Ireland | No (use consent mode) | S.I. 336/2011 reg. 5(3) and the DPC: analytics need consent. The DPC says first-party aggregate analytics with an opt-out are unlikely to be an enforcement priority; that is discretion, not an exemption. | S27, high |
| Austria, Belgium, Croatia, Cyprus, Czechia, Denmark, Latvia, Norway, Poland, Slovakia, Slovenia, Sweden | No (use consent mode) | The statute has only the transmission and strictly necessary exemptions, and the regulator says analytics or statistics need consent or are not strictly necessary. | Austria S15, Belgium S16, Croatia S18, Cyprus S19, Czechia S20, Denmark S21, Latvia S28, Norway S33, Poland S34, Slovakia S37, Slovenia S38, Sweden S40; high (Austria medium-high) |
| Estonia, Greece, Hungary, Iceland, Malta, Portugal, Romania | No (use consent mode) | Same statutory pattern; the regulator requires consent or reads the exemptions narrowly. A gap remains in each: Estonia (how ESS § 102 maps to Art. 5(3)), Greece (statute read to 2019; guidance worded for third-party tools), Hungary (exemptions not found in § 155; guidance from 2017), Iceland (current consolidated text not read), Malta (IDPC silent on analytics), Portugal (statute read to 2012), Romania (text read on the DPA's copy). | Estonia S22, Greece S24, Hungary S25, Iceland S26, Malta S32, Portugal S35, Romania S36; medium |
| Lithuania | Unconfirmed | The official statute and VDAI sites returned HTTP 403. Unofficial copies point to consent for all but strictly necessary cookies. Use consent mode. | S30, low |
| Turkey | Possibly, with conditions | The current KVKK cookie guide (July 2025 edition), section 5.9, places first-party analytics under "Kriter B", outside explicit consent, when used only for anonymous statistics and not for cross-site tracking. The service runs as a third party on the tenant's behalf; the guide does not address third-party analytics as such. Use consent mode unless your counsel confirms the guide covers your setup. | S7, high on the text, open on its application |
| United States (CCPA/CPRA) | Yes, with conditions | California is an opt-out regime; no opt-in applies to this analytics. The Act binds only tenants that are a "business" (Civ. Code § 1798.140(d)). Conditions: a notice at collection listing categories, purposes, that nothing is sold or shared, and retention (§ 1798.100(a), 11 CCR § 7012); a written service-provider contract (§ 1798.140(ag), § 1798.100(d), 11 CCR § 7051(a)), without which disclosure may count as a sale; the operator does not combine data across tenants or use it for itself. The minors' opt-in (§ 1798.120(c)) covers only sale or sharing, which does not happen. GPC need not be honoured where nothing is sold or shared; the tracker honours it anyway. | S12, high |
| Brazil (LGPD) | Yes, with conditions | Legitimate interest (Art. 7 IX, Art. 10). The ANPD cookie guide accepts it for audience measurement based on aggregate data, without profiles or combination with other tracking. Conditions: the tenant documents a short balancing test before starting; only strictly necessary data; transparency in the privacy notice; a direct opt-out link, not only browser settings; the operator acts on the tenant's instructions (Art. 39); a data protection impact report on the ANPD's request. | S13, high on text, medium-high on application |

## What tenants must do in every jurisdiction

- Name the service and the processing in the site's privacy notice.
- Offer a simple opt-out link that sets the opt-out flag
  (`localStorage.setItem('umami.disabled', '1')`).
- Put no personal data in URLs, page titles, referrers or event data (`terms.md`).
- Where in doubt, use consent mode.

## Pending EU change

The Commission's Digital Omnibus proposal COM(2025) 837 (procedure
2025/0360(COD)) would add a GDPR Art. 88a allowing storage or access without
consent for aggregated audience measurement by the controller of an online
service for its own use.

Status on 2026-11-13 (S14):

- It is a proposal at first reading and has no legal effect. National Art. 5(3)
  laws apply unchanged.
- Parliament: ITRE and LIBE (joint committee) have a draft report (22 June
  2026) but have not voted. OEIL shows "Awaiting committee decision".
- Council: Coreper agreed its negotiating mandate on 21 October 2026 (ST
  14756/26). In that text Art. 88a appears to be deleted, and the audience
  measurement exemption moves into ePrivacy Art. 5(3), narrowed to "anonymous
  aggregated information", allowed for a third party acting for the provider,
  with no other purpose, sharing or combination. Member States would have 24
  months to transpose.
- Trilogues cannot start until Parliament adopts its position. The final text
  may differ from both versions.

The separate AI omnibus (COM(2025) 836) became Regulation (EU) 2026/1744; it
does not contain Art. 88a.
