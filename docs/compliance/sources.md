# Legal sources register

Status: draft, pending legal review an operator input. Not legal advice. Date checked: 2026-11-12.

This register records what each primary source actually says, as read on the date above. Other documents cite the anchors below (S1, S2, ...). Leads came from `.scratch/report.md` section 7; where a primary source disagrees with that report, the entry says so. Statements marked "Inference" are our reading, not the source's words.

---

<a id="S1"></a>
## S1. EDPB Guidelines 2/2023 on the technical scope of Art. 5(3) ePrivacy Directive

**Supported claim.**
- Version 2.0 was "Adopted on 7 October 2024". The version history lists Version 1.0, 14 November 2023, "Adoption of the Guidelines for public consultation", and Version 2.0, 7 October 2024, "Adoption of the Guidelines after public consultation". The EDPB page lists the final version under 16 October 2024.
- The use cases covered include "URL and pixel tracking" (section 3.1), "Local processing" (3.2), "Tracking based on IP only" (3.3), "Intermittent and mediated IoT reporting" (3.4) and "Unique Identifier" (3.5).
- Para 2: WP29 Opinion 9/2014 "has already clarified that fingerprinting" falls within scope.
- On scripts instructing the browser (para 33): "Additional examples would include JavaScript code, where the accessing entity instructs the browser of the user to send asynchronous requests with the targeted information. Such access clearly falls within the scope of Article 5(3) ePD". Para 51: for dynamically constructed tracking pixels, "it is the distribution of the applicative logic (usually a JavaScript code) that constitutes the instruction", and the collection "constitutes a 'gaining of access'".
- Para 53 (local processing): sending locally produced information back to a server "would constitute a 'gaining of access to information already stored'".
- Paras 54 and 55 (IP only): Art. 5(3) applies where the IP address originates from the terminal equipment. "Unless the entity can ensure that the IP address does not originate from the terminal equipment of a user or subscriber, it has to take all the steps pursuant to the Article 5(3) ePD."
- Para 56: the guidelines "do not analyse the application of the exemptions"; applicability "does not systematically mean that consent needs to be collected".

**Note on report.md.** The report calls "a tracker instructing the browser to send information is gaining access" an inference. The guidelines say this directly (para 33). The conclusion that cookieless analytics therefore depends on national exemptions is still our inference, supported by para 56.

**Sources.**
- https://www.edpb.europa.eu/our-work-tools/our-documents/guidelines/guidelines-22023-technical-scope-art-53-eprivacy-directive_en
- https://www.edpb.europa.eu/system/files/documents/2024-10/edpb_guidelines_202302_technical_scope_art_53_eprivacydirective_v2_en_0.pdf

Date checked: 2026-11-12. Confidence: high. Status: confirmed.

---

<a id="S2"></a>
## S2. France (CNIL): audience measurement consent exemption

**Supported claim.**
- Legal basis of the exemption: CNIL délibération n° 2020-091 of 17 September 2020 (lignes directrices on Article 82 of the loi Informatique et Libertés), article 5, paras 50 to 52. Para 50: trackers whose purpose is limited to audience measurement are "strictement nécessaires au fonctionnement et aux opérations d'administration courante" and are not subject to prior consent.
- Conditions (para 51): purpose "strictement limitée à la seule mesure de l'audience sur le site ou l'application pour le compte exclusif de l'éditeur"; must not allow "le suivi global de la navigation" across sites or apps; must "uniquement servir à produire des données statistiques anonymes"; personal data "ne peuvent être recoupées avec d'autres traitements ni transmises à des tiers". Para 52: audience measurement remains processing of personal data subject to the GDPR.
- Scope beyond cookies: para 13 of the same guidelines says they cover HTTP cookies and also "local storage", "les identifications par calcul d'empreinte du terminal ou « fingerprinting »", OS identifiers and hardware identifiers; "traceur" means every device covered by Article 82. The July 2025 self-assessment tool expressly contemplates fingerprint-based identifiers (see below).
- Self-assessment tool: "Outil d'auto-évaluation relatif à la mise en œuvre d'une solution de mesure d'audience exemptée de consentement", dated "Juillet 2025", addressed to the provider of the solution. Criteria it lists include:
  - provider offers the service "sous le régime de la sous-traitance" with a standard DPA containing the Article 28 GDPR clauses;
  - "Aucune mise en commun par le prestataire de données brutes de mesure d'audience provenant de plusieurs de ses clients";
  - no reuse of data for the provider's own account, whatever the purpose;
  - no import of external data; referrer, if collected, limited to the host; any integration with third-party tools excluded;
  - IP, if used, pseudonymised by removing at least the last octet;
  - any fingerprint-based identifier must include a site-specific component (for example the domain) and a temporal component so it has a limited lifetime;
  - reports contain only anonymous statistics, recommended aggregation "à la dizaine la plus proche", and anonymisation must hold whatever filter combination the client chooses;
  - session replay disabled; an objection mechanism (button or link in the privacy policy) where personal data are processed, kept effective over time (opt-out cookie or fingerprint blocklist).
  - Reuse: reuse of collected data needs no consent "à la condition qu'elles aient été préalablement anonymisées de manière effective"; the GDPR still applies to the anonymisation step.
- Recommendation (consolidated version published 16 January 2026, from délibération n° 2020-092 and délibération n° 2025-131 of 18 December 2025): for exempt audience measurement trackers, inform users, limit tracker lifetime to about thirteen months without automatic extension, keep collected data for at most twenty-five months, and review these periods periodically. Délibération 2025-131 concerns multi-device consent, not the audience measurement exemption.
- The CNIL page on audience measurement solutions is dated 4 July 2025.

**Contradictions with report.md.**
- The report cites a "deliberation of 4 July 2025". We found no deliberation of that date. 4 July 2025 is the date of the CNIL web page; the self-assessment tool is dated July 2025. The exemption itself rests on délibération 2020-091.
- The report cites a compliance deadline of 1 January 2026 from a secondary source. No such deadline appears on the CNIL page, in the tool or in the guidelines we read. Unconfirmed.

**Sources.**
- https://www.cnil.fr/sites/cnil/files/atoms/files/lignes_directrices_de_la_cnil_sur_les_cookies_et_autres_traceurs.pdf
- https://www.cnil.fr/fr/cookies-et-autres-traceurs/regles/cookies-solutions-pour-les-outils-de-mesure-daudience
- https://www.cnil.fr/sites/default/files/2025-07/outil_d_auto-evaluation_mesure_d_audience.pdf
- https://www.cnil.fr/sites/default/files/2026-01/recommandation_cookies_consolidee.pdf

Date checked: 2026-11-12. Confidence: high on conditions, scope and tool; low on any deadline. Status: partly confirmed (exemption, conditions, non-cookie scope and July 2025 tool confirmed; "deliberation of 4 July 2025" and 1 January 2026 deadline not confirmed).

---

<a id="S3"></a>
## S3. United Kingdom: Data (Use and Access) Act 2025 and the PECR statistical purposes exception

**Supported claim.**
- Royal Assent: 19 June 2025 (Data (Use and Access) Act 2025 c. 18).
- Section 112 ("Storing information in the terminal equipment of a subscriber or user") substitutes PECR regulation 6, inserts regulation 6A (power to vary exceptions) and, by subsection (4), gives effect to Schedule 12, which inserts Schedule A1 into PECR containing the exceptions.
- The statistical purposes exception is paragraph 5 of new PECR Schedule A1 (inserted by Schedule 12). Conditions, in the statute's words: sole purpose of collecting statistics about use of the service or website with a view to improving it; information "not shared with any other person except for the purpose of enabling that other person to assist" with that improvement; "clear and comprehensive information about the purpose of the storage or access"; and "the subscriber or user is given a simple means of objecting, free of charge, to the storage or access and does not object".
- Commencement: The Data (Use and Access) Act 2025 (Commencement No. 6 and Transitional and Saving Provisions) Regulations 2026, SI 2026/82, made 29 January 2026, regulation 2(w) (section 112) and 2(z13) (Schedule 12): in force 5 February 2026. Section 112 was already in force at Royal Assent for specified purposes (s. 142(1)(2)(h)).
- ICO "Guidance on the use of storage and access technologies": first published 20 December 2024; updated 7 July 2025 "to reflect changes to PECR following the Data (Use and Access) Act"; finalised 29 April 2026. The "What are the exceptions?" chapter says, for the statistical purposes exception, that a third-party analytics provider "must be a processor, not a joint controller", that users must be told a third-party provider is used, that you "must not solely rely on browser settings" to determine objection, and that personal data must not be stored longer than necessary.

**Note on report.md.** Dates (Royal Assent 19 June 2025, in force 5 February 2026, ICO final 29 April 2026) and the section number (s. 112) match. Precision: the exception text itself is in Schedule 12 (new PECR Sch. A1 para 5), brought in by s. 112(4). The "vendors must act only for the controller" condition, which the report took from secondary analyses, is supported by the ICO's processor statement and by the statutory sharing condition.

**Sources.**
- https://www.legislation.gov.uk/ukpga/2025/18/introduction
- https://www.legislation.gov.uk/ukpga/2025/18/section/112
- https://www.legislation.gov.uk/ukpga/2025/18/schedule/12
- https://www.legislation.gov.uk/uksi/2026/82/regulation/2/made
- https://ico.org.uk/for-organisations/direct-marketing-and-privacy-and-electronic-communications/guidance-on-the-use-of-storage-and-access-technologies/
- https://ico.org.uk/for-organisations/direct-marketing-and-privacy-and-electronic-communications/guidance-on-the-use-of-storage-and-access-technologies/what-are-the-exceptions/

Date checked: 2026-11-12. Confidence: high. Status: confirmed.

---

<a id="S4"></a>
## S4. Germany: TDDDG section 25

**Supported claim.**
- § 25(1) TDDDG: storing or accessing information in end-user terminal equipment is permitted only with consent based on clear and comprehensive information, given in accordance with the GDPR.
- § 25(2) has two exemptions only: no. 1, where the sole purpose is "die Durchführung der Übertragung einer Nachricht über ein öffentliches Telekommunikationsnetz"; no. 2, where the storage or access is "unbedingt erforderlich ist, damit der Anbieter eines digitalen Dienstes einen vom Nutzer ausdrücklich gewünschten digitalen Dienst zur Verfügung stellen kann". There is no analytics or reach measurement exemption in the text.
- DSK "Orientierungshilfe der Aufsichtsbehörden für Anbieter:innen von digitalen Diensten (OH Digitale Dienste)", Version 1.2, Stand November 2024, paras 87 to 90: the authorities deliberately make no general statement that "Reichweitenmessung" falls under § 25(2) no. 2. A finding is possible "allenfalls für eine genau definierte Konfiguration und Zweckbestimmung" (para 89). "Selbst die einfache Messung von Besucherzahlen ist daher nicht per se als Bestandteil des Basisdienstes einzustufen, sondern abhängig vom jeweils konkret verfolgten Zweck" (para 90). Para 88 notes that pure page impression counting can be done by a simple counter, by log files without personal data, or by a simple first-party counting pixel that collects no further user data.
- Reform: we found no German reform adding an analytics exemption to § 25. At EU level, the Commission's Digital Omnibus proposal COM(2025) 837 (19 November 2025) would insert GDPR Article 88a, whose para 3(c) allows storage or access without consent where necessary for "creating aggregated information about the usage of an online service to measure the audience of such a service, where it is carried out by the controller of that online service solely for its own use". This is a proposal; we did not verify its legislative status as of 2026-11-12, and it covers personal data in terminal equipment, not § 25 TDDDG as such.

**Note on report.md.** Consistent with the report ("only a strictly-necessary exemption; strict reading means consent"). The DSK position is more precise: no blanket answer, case by case by configuration and purpose.

**Sources.**
- https://www.gesetze-im-internet.de/ttdsg/__25.html
- https://datenschutzkonferenz-online.de/media/oh/OH_Digitale_Dienste.pdf
- https://eur-lex.europa.eu/legal-content/EN/TXT/HTML/?uri=CELEX:52025PC0837

Date checked: 2026-11-12. Confidence: high on statute and DSK text; low on Omnibus status. Status: confirmed (statute and DSK position); reform status partly confirmed (proposal text read, adoption status not checked).

---

<a id="S5"></a>
## S5. Netherlands: Telecommunicatiewet article 11.7a

**Supported claim.**
- Art. 11.7a(1) (version in force from 15 August 2026) applies to "het via een elektronisch communicatienetwerk opslaan van of toegang verkrijgen tot informatie in de randapparatuur van een gebruiker", requiring information and consent. The rule is technology-neutral: it is not limited to cookies.
- Art. 11.7a(3)(b) exempts storage or access that is strictly necessary for the requested information society service "of, mits dit geen of geringe gevolgen heeft voor de persoonlijke levenssfeer van de betrokken abonnee of gebruiker, om informatie te verkrijgen over de kwaliteit of effectiviteit van een geleverde dienst van de informatiemaatschappij".
- Art. 11.7a(4): an operation aimed at collecting, combining or analysing data about the user's use of different information society services so the user can be treated differently is presumed to be processing of personal data.
- Autoriteit Persoonsgegevens (English cookies page): "limited analytics cookies" (number of visitors, most visited pages) with no or little impact on privacy need no consent, citing Article 11.7a; if they process personal data, a legal basis is needed and the use must be stated in the privacy statement.

**Not confirmed.** We did not find AP guidance specifically on non-cookie techniques; the statement that the exemption covers cookieless access rests on the statutory wording only (Inference: the exemption follows the same "opslaan van of toegang verkrijgen tot" scope as para 1). The AP page's further conditions (for example on sharing with third parties) were not read in full; one AP URL returned 404.

**Sources.**
- https://wetten.overheid.nl/jci1.3:c:BWBR0009950&hoofdstuk=11&artikel=11.7a
- https://www.autoriteitpersoonsgegevens.nl/en/themes/internet-and-smart-devices/cookies

Date checked: 2026-11-12. Confidence: high on statute; medium on AP practice for cookieless methods. Status: partly confirmed (statutory exemption and AP summary confirmed; AP guidance on non-cookie access not found).

---

<a id="S6"></a>
## S6. Italy: Garante guidelines on cookies and other tracking tools (10 June 2021)

**Supported claim.**
- Garante provvedimento of 10 June 2021, register n. 231 ("Linee guida cookie e altri strumenti di tracciamento"), published in Gazzetta Ufficiale n. 163 of 9 July 2021 (docweb 9677876).
- Section 7.2: analytics cookies can be treated like technical cookies (no consent) only where: they cannot lead to "diretta individuazione dell'interessato"; they are used "unicamente alla produzione di statistiche aggregate"; they are used for a single site or app so browsing on other sites cannot be tracked; where third parties provide the service, they do not combine the minimised data with other processing or pass it to further third parties (an exception exists for statistics across several domains of the same publisher or group); for third-party tools, minimisation includes masking at least the fourth component of IPv4 addresses (and equivalent for IPv6).

**Note on report.md.** The report's "Italy's Garante has accepted anonymised analytics under conditions" is consistent. The detail above was read via a summarising fetch of the docweb page, not a full manual read of section 7.2; quote it from the source before relying on exact wording.

**Sources.**
- https://www.garanteprivacy.it/home/docweb/-/docweb-display/docweb/9677876

Date checked: 2026-11-12. Confidence: medium. Status: confirmed (conditions), wording to be rechecked against the full text.

---

<a id="S7"></a>
## S7. Turkey: KVKK cookie guide and cross-border transfer rules

**Supported claim.**
- Cookie guide: "Çerez Uygulamaları Hakkında Rehber", KVKK Yayınları No: 69, the edition served on 2026-11-12 is dated "Temmuz 2025, Ankara". Section 4 sets Kriter A (cookie used solely to carry out communication over an electronic communications network) and Kriter B (strictly necessary for an information society service explicitly requested). Section 5 is titled "Açık rıza dışındaki diğer işleme şartları dahilinde çerez kullanım senaryoları" (cookie scenarios under processing conditions other than explicit consent). Section 5.9, "Birinci Taraf Analitik Çerezler (Kriter B)", states that first-party analytics "Kriter B kapsamında değerlendirilebileceği düşünülmektedir", provided they are used only to produce anonymous statistics and not for cross-site tracking; IP masking and not passing data to third parties are recommended. Section 6 (explicit consent scenarios) lists social plug-in tracking cookies and online behavioural advertising cookies.
- Law No. 7499 (accepted 2 March 2024, Resmî Gazete No. 32487, 12 March 2024) amends Article 9 of Law No. 6698. New Art. 9(5): "Standart sözleşme, imzalanmasından itibaren beş iş günü içinde veri sorumlusu veya veri işleyen tarafından Kuruma bildirilir." Article 40(1)(a) of Law 7499 puts the relevant articles into force on 1 June 2024.
- "Kişisel Verilerin Yurt Dışına Aktarılmasına İlişkin Usul ve Esaslar Hakkında Yönetmelik", Resmî Gazete 10 July 2024, No. 32598. Article 14(5): the standard contract is notified to the Authority within five business days of completion of signatures, physically, by KEP, or by other methods the Board sets.
- EU adequacy: the European Commission's list of adequacy decisions (17 entries) does not include Turkey.

**Contradiction with report.md.** The report says the KVKK Board cookie guidance "treats analytics cookies as needing explicit consent". The current official edition (July 2025) says the opposite for first-party analytics: they can fall under Kriter B, outside explicit consent, under conditions (anonymous statistics, no cross-site tracking). We did not obtain the original 2022 edition to check whether it differed. Third-party analytics are not addressed as such in section 5.9.

**Not confirmed.** Board decision 2024/959 of 4 June 2024 cited by the report was not checked.

**Sources.**
- https://kvkk.gov.tr/Icerik/7353/Cerez-Uygulamalari-Hakkinda-Rehber
- https://www.resmigazete.gov.tr/eskiler/2024/03/20240312-1.htm
- https://www.resmigazete.gov.tr/eskiler/2024/07/20240710-2.htm
- https://commission.europa.eu/law/law-topic/data-protection/international-dimension-data-protection/adequacy-decisions_en

Date checked: 2026-11-12. Confidence: high (guide text, Law 7499, Regulation, adequacy list). Status: partly confirmed (transfer rules and no-adequacy confirmed; cookie guide claim contradicted).

---

<a id="S8"></a>
## S8. CJEU C-413/23 P, EDPS v SRB

**Supported claim.**
- Judgment of the Court (First Chamber), 4 September 2025.
- Para 86: pseudonymised data must not be regarded as constituting personal data in all cases and for every person, since pseudonymisation may, depending on the circumstances, "effectively prevent persons other than the controller from identifying the data subject".
- Para 77: for a recipient (Deloitte) the data may not be personal where it cannot lift the pseudonymisation measures and cannot re-identify by other means. Para 85: where third parties can re-identify, the data remain personal; the recipient's position does not change the controller's assessment.
- Paras 111 and 112: for the information obligation, identifiability "must be assessed at the time of collection of the data" and from the controller's point of view.
- Operative part: sets aside the General Court judgment of 26 April 2023 (T-557/20), refers the case back to the General Court, reserves costs.

**Inference (ours, not the Court's).** For a salted hash of IP and user agent: while the operator holds or can recompute the salt, the operator can re-identify in the relevant sense, so the hashes are personal data for the operator. Once a random salt is destroyed and cannot be recomputed by anyone, the argument for treating stored hashes as non-personal relative to the operator becomes stronger, but under paras 111 and 112 the controller's obligations at collection time (while the salt existed) still apply. This should be reviewed by counsel.

**Sources.**
- https://eur-lex.europa.eu/legal-content/EN/TXT/HTML/?uri=CELEX:62023CJ0413

Date checked: 2026-11-12. Confidence: high on judgment; inference unreviewed. Status: confirmed.

---

<a id="S9"></a>
## S9. EU-US Data Privacy Framework litigation (Latombe)

**Supported claim.**
- General Court (Tenth Chamber, Extended Composition), judgment of 3 September 2025, T-553/23 Latombe v Commission: "Dismisses the action"; Mr Latombe bears his own costs and the Commission's.
- Appeal C-703/25 P: "Appeal brought on 31 October 2025 by Philippe Latombe", published in OJ C on 22 December 2025 (C/2025/6610). Pleas concern the independence and establishment by law of the Data Protection Review Court, bulk collection, and presidential power under Executive Order 14086.
- InfoCuria on 2026-11-12 shows the case as "Pending" before the Court of Justice, with an Order of 4 June 2026 (ECLI:EU:C:2026:465). We could not load the order's text (EUR-Lex returned "document does not exist"), so its subject is unknown. No judgment is listed.

**Note on report.md.** Matches the report (3 September 2025, appeal filed 31 October 2025, pending). The 4 June 2026 order was not mentioned in the report.

**Sources.**
- https://eur-lex.europa.eu/legal-content/EN/TXT/HTML/?uri=CELEX:62023TJ0553
- https://eur-lex.europa.eu/eli/C/2025/6610/oj/eng
- https://infocuria.curia.europa.eu/tabs/redirect/juris/liste.jsf?num=C-703/25&language=en

Date checked: 2026-11-12. Confidence: high. Status: confirmed (order content unconfirmed).

---

<a id="S10"></a>
## S10. GDPR Article 11 and Article 28 (Regulation (EU) 2016/679)

**Supported claim.**
- Art. 11(1): "If the purposes for which a controller processes personal data do not or do no longer require the identification of a data subject by the controller, the controller shall not be obliged to maintain, acquire or process additional information in order to identify the data subject for the sole purpose of complying with this Regulation."
- Art. 11(2): where the controller can demonstrate it cannot identify the data subject, it informs the data subject if possible, and Articles 15 to 20 do not apply unless the data subject provides additional information enabling identification.
- Art. 28(3): the contract must set out subject-matter and duration, nature and purpose, type of personal data, categories of data subjects, and the controller's obligations and rights, and must stipulate that the processor: (a) processes only on documented instructions, including on third-country transfers; (b) ensures confidentiality of authorised persons; (c) takes all measures required by Article 32; (d) respects the conditions of paras 2 and 4 for sub-processors; (e) assists with data subject requests; (f) assists with Articles 32 to 36; (g) at the controller's choice deletes or returns all personal data at the end of services; (h) makes information available and allows for and contributes to audits. The processor must immediately inform the controller if an instruction infringes data protection law. Art. 28(2) and (4) govern sub-processor authorisation and flow-down.

**Sources.**
- https://eur-lex.europa.eu/legal-content/EN/TXT/HTML/?uri=CELEX:32016R0679

Date checked: 2026-11-12. Confidence: high. Status: confirmed.

---

<a id="S11"></a>
## S11. Other jurisdictions stated in report.md section 7

**US (CCPA/CPRA): "no opt-in for first-party analytics".** Not confirmed. The California Legislative Information page for Civil Code § 1798.140 returned HTTP 403, and no other primary source was read.

**Brazil (LGPD): "legitimate interest available".** Not confirmed. The Planalto URLs for Lei 13.709/2018 redirected to the gov.br home page, so the text of Art. 7 was not read.

Date checked: 2026-11-12. Confidence: low. Status: unconfirmed (primary source unreachable).

---

## Summary

| S-id | Claim | Status | Confidence |
|---|---|---|---|
| S1 | EDPB Guidelines 2/2023 v2.0 scope, JavaScript instruction is access | Confirmed | High |
| S2 | CNIL audience measurement exemption, tool, non-cookie scope | Partly confirmed (no 4 July 2025 deliberation, no 1 Jan 2026 deadline found) | High (conditions), low (deadline) |
| S3 | UK DUAA 2025 s.112 / Sch. 12, in force 5 Feb 2026, ICO guidance | Confirmed | High |
| S4 | Germany § 25 TDDDG, DSK on reach measurement, reform | Confirmed (law, DSK); reform status partly confirmed | High (law, DSK), low (Omnibus status) |
| S5 | Netherlands Tw art. 11.7a low-impact analytics | Partly confirmed (AP guidance on non-cookie not found) | High (statute), medium (AP) |
| S6 | Italy Garante 10 June 2021 analytics conditions | Confirmed, wording to recheck | Medium |
| S7 | Turkey cookie guide, Law 7499, 2024 Regulation, no EU adequacy | Partly confirmed; cookie claim contradicted | High |
| S8 | CJEU C-413/23 P EDPS v SRB | Confirmed (application is inference) | High |
| S9 | Latombe T-553/23 and appeal C-703/25 P | Confirmed (pending; order of 4 June 2026 not read) | High |
| S10 | GDPR Art. 11 and Art. 28 | Confirmed | High |
| S11 | US CCPA and Brazil LGPD claims | Unconfirmed | Low |
