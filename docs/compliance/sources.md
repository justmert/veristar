---
title: "Legal sources register"
description: "The register of legal sources the compliance documents rely on."
icon: "book-bookmark"
---

This register records what each primary source says, as read on the date stated in its entry (2026-11-12 or 2026-11-13). The other compliance pages cite the anchors below (S1, S2, ...). An anchor never changes its meaning, so a retired number (S11) is not reused. Statements marked "Inference" are a reading of the source, not the source's words. This page is not legal advice.

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

**Inference.** That cookieless analytics therefore depends on national exemptions follows from paras 33 and 56; the guidelines do not state it in those words.

**Sources.**
- https://www.edpb.europa.eu/our-work-tools/our-documents/guidelines/guidelines-22023-technical-scope-art-53-eprivacy-directive_en
- https://www.edpb.europa.eu/system/files/documents/2024-10/edpb_guidelines_202302_technical_scope_art_53_eprivacydirective_v2_en_0.pdf

Date checked: 2026-11-12. Confidence: high. Status: confirmed.

---

<a id="S2"></a>
## S2. France (CNIL): audience measurement consent exemption

**Supported claim.**
- Legal basis of the exemption: CNIL délibération n° 2020-091 of 17 September 2020 (lignes directrices on Art. 82 of the loi Informatique et Libertés), Art. 5, paras 50 to 52. Para 50: trackers whose purpose is limited to audience measurement are "strictement nécessaires au fonctionnement et aux opérations d'administration courante" and are not subject to prior consent.
- Conditions (para 51): purpose "strictement limitée à la seule mesure de l'audience sur le site ou l'application pour le compte exclusif de l'éditeur"; must not allow "le suivi global de la navigation" across sites or apps; must "uniquement servir à produire des données statistiques anonymes"; personal data "ne peuvent être recoupées avec d'autres traitements ni transmises à des tiers". Para 52: audience measurement remains processing of personal data subject to the GDPR.
- Scope beyond cookies: para 13 of the same guidelines says they cover HTTP cookies and also "local storage", "les identifications par calcul d'empreinte du terminal ou « fingerprinting »", OS identifiers and hardware identifiers; "traceur" means every device covered by Art. 82. The July 2025 self-assessment tool expressly contemplates fingerprint-based identifiers (see below).
- Self-assessment tool: "Outil d'auto-évaluation relatif à la mise en œuvre d'une solution de mesure d'audience exemptée de consentement", dated "Juillet 2025", addressed to the provider of the solution. Criteria it lists include:
  - provider offers the service "sous le régime de la sous-traitance" with a standard DPA containing the Art. 28 GDPR clauses;
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

**The referrer and rounding criteria, quoted from the primary text (read 2026-11-13).** The tool is an eight-page PDF; section 4 ("Critères de mise en œuvre de la mesure d’audience", page 3) is a table of Objectif, Critère and Mesure technique. Its preamble says how to read the third column:

> "Afin d’établir que l’outil de mesure d’audience rentre dans le cadre de l’outil d’auto-évaluation, celui-ci doit pouvoir être configuré afin de respecter l’ensemble des critères du tableau ci-dessous. Quand le tableau indique une mesure technique précise, il s’agit d’une recommandation de la CNIL. Il est possible pour le prestataire de démontrer qu’une mesure alternative remplit effectivement l’objectif. Dans ce cas, il lui est recommandé de l’indiquer et de le justifier dans la documentation qu’il fournit à ses potentiels clients (en étant clair sur le fait qu’il s’écarte des mesures recommandées par la CNIL)."

Translation: to fall within the tool, the solution must be configurable to meet all the criteria of the table; where the table names a precise technical measure, that is a CNIL recommendation; the provider may show that an alternative measure meets the objective and should then say so and justify it in its documentation, making clear that it departs from the recommended measures.

- Referrer (page 4). Objectif: "L’outil ne permet pas un suivi de la personne en dehors du site ou de l’application visée". Critère: "Aucun import de données externes n’est possible". Mesure technique, three bullets: "Désactivation de toute collecte ou import d’identifiant client (ou « CRM »), d’UTM ou d’identifiant de campagne dans les URLs"; "Le référent (« referrer »), s’il est collecté, se limite au domaine (« host »)."; "Toute intégration avec des outils tiers est exclue".
- Rounding (page 4). Objectif: "Les données servent uniquement à produire des données statistiques anonymes". Critère: "Autant pour la visualisation mise à disposition dans l’interface de l’outil que lors d’export, l’ensemble des rapports générés par la solution comportent uniquement des statistiques anonymes". Mesure technique: "Agrégation et la présentation à la dizaine la plus proche. A défaut, une analyse est menée pour justifier du caractère anonyme des données (voir l’avis du G29 sur le sujet)". The next row adds the criterion "L’anonymisation est effective quels que soient les critères de sélection choisi par le client de la solution (une combinaison de critère ne doit pas permettre d’isoler un utilisateur)", with no technical measure; the row after it, "Aucun suivi de la navigation d’un utilisateur unique n’est possible", names "Désactivation de toute fonctionnalités du type rejeu de session (« session replay »)".
- The CNIL page of 4 July 2025 says the tool sets out sufficient, not necessary, conditions: "Celui-ci détaille les conditions suffisantes pour que l’exemption s’applique. Pour autant, il n’est pas exclu que certaines solutions ne remplissant pas l’ensemble des critères définis puissent tout de même être mises en œuvre de sorte à respecter l’exemption prévue par l’article 82 de la loi Informatique et Libertés." It puts the burden on the provider: "Il serait alors de la responsabilité du fournisseur de documenter son analyse pour démontrer que, bien que sortant du champ de l’outil d’auto-évaluation fourni par la CNIL, sa solution, telle que configurée, peut être mise en œuvre sans recueillir le consentement." It also warns that a self-assessment "ne préjuge pas de l’analyse que la CNIL pourra en faire".

**What is required, and of what kind (inference).**
- Neither "referrer limited to the host" nor "rounded to the nearest ten" is a condition of the exemption in its own right. The conditions are those of délibération 2020-091 Art. 5 (para 51 above). The tool's criteria are a sufficient route to them, and each named technical measure is a recommendation that an alternative may replace if the provider shows the alternative meets the objective.
- Referrer: the objective is that no outside data is imported. The recommended measure is to keep only the host. Following it is the only route that needs no further justification.
- Rounding: the criterion (all reports and exports contain only anonymous statistics, for any combination of filters) is what the tool requires. Rounding to ten is the recommended measure; the alternative is a documented anonymity analysis under WP29 Opinion 05/2014 on anonymisation techniques. Rounding alone does not satisfy the next criterion (no combination of filters isolates a user).
- The same row as the referrer measure recommends disabling the collection of UTM and campaign identifiers in URLs.

**Sources.**
- https://www.cnil.fr/sites/cnil/files/atoms/files/lignes_directrices_de_la_cnil_sur_les_cookies_et_autres_traceurs.pdf
- https://www.cnil.fr/fr/cookies-et-autres-traceurs/regles/cookies-solutions-pour-les-outils-de-mesure-daudience
- https://www.cnil.fr/sites/default/files/2025-07/outil_d_auto-evaluation_mesure_d_audience.pdf
- https://www.cnil.fr/sites/default/files/2026-01/recommandation_cookies_consolidee.pdf

Date checked: 2026-11-12; tool and page reread for the referrer and rounding criteria on 2026-11-13. Confidence: high on conditions, scope and tool. Status: confirmed (exemption, conditions, non-cookie scope and July 2025 tool).

---

<a id="S3"></a>
## S3. United Kingdom: Data (Use and Access) Act 2025 and the PECR statistical purposes exception

**Supported claim.**
- Royal Assent: 19 June 2025 (Data (Use and Access) Act 2025 c. 18).
- Section 112 ("Storing information in the terminal equipment of a subscriber or user") substitutes PECR regulation 6, inserts regulation 6A (power to vary exceptions) and, by subsection (4), gives effect to Schedule 12, which inserts Schedule A1 into PECR containing the exceptions.
- The statistical purposes exception is paragraph 5 of new PECR Schedule A1 (inserted by Schedule 12). Conditions, in the statute's words: sole purpose of collecting statistics about use of the service or website with a view to improving it; information "not shared with any other person except for the purpose of enabling that other person to assist" with that improvement; "clear and comprehensive information about the purpose of the storage or access"; and "the subscriber or user is given a simple means of objecting, free of charge, to the storage or access and does not object".
- Commencement: The Data (Use and Access) Act 2025 (Commencement No. 6 and Transitional and Saving Provisions) Regulations 2026, SI 2026/82, made 29 January 2026, regulation 2(w) (section 112) and 2(z13) (Schedule 12): in force 5 February 2026. Section 112 was already in force at Royal Assent for specified purposes (s. 142(1)(2)(h)).
- ICO "Guidance on the use of storage and access technologies": first published 20 December 2024; updated 7 July 2025 "to reflect changes to PECR following the Data (Use and Access) Act"; finalised 29 April 2026. The "What are the exceptions?" chapter says, for the statistical purposes exception, that a third-party analytics provider "must be a processor, not a joint controller", that users must be told a third-party provider is used, that you "must not solely rely on browser settings" to determine objection, and that personal data must not be stored longer than necessary.

**Note.** Royal Assent 19 June 2025, in force 5 February 2026, ICO guidance final 29 April 2026. The exception text itself is in Schedule 12 (new PECR Sch. A1 para 5), brought in by s. 112(4). The condition that an analytics vendor acts only for the controller follows from the ICO's processor statement and from the statutory sharing condition.

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
## S4. Germany: § 25 TDDDG

**Supported claim.**
- § 25(1) TDDDG: storing or accessing information in end-user terminal equipment is permitted only with consent based on clear and comprehensive information, given in accordance with the GDPR.
- § 25(2) has two exemptions only: no. 1, where the sole purpose is "die Durchführung der Übertragung einer Nachricht über ein öffentliches Telekommunikationsnetz"; no. 2, where the storage or access is "unbedingt erforderlich ist, damit der Anbieter eines digitalen Dienstes einen vom Nutzer ausdrücklich gewünschten digitalen Dienst zur Verfügung stellen kann". There is no analytics or reach measurement exemption in the text.
- DSK "Orientierungshilfe der Aufsichtsbehörden für Anbieter:innen von digitalen Diensten (OH Digitale Dienste)", Version 1.2, Stand November 2024, paras 87 to 90: the authorities deliberately make no general statement that "Reichweitenmessung" falls under § 25(2) no. 2. A finding is possible "allenfalls für eine genau definierte Konfiguration und Zweckbestimmung" (para 89). "Selbst die einfache Messung von Besucherzahlen ist daher nicht per se als Bestandteil des Basisdienstes einzustufen, sondern abhängig vom jeweils konkret verfolgten Zweck" (para 90). Para 88 notes that pure page impression counting can be done by a simple counter, by log files without personal data, or by a simple first-party counting pixel that collects no further user data.
- Reform: no German reform was found adding an analytics exemption to § 25. At EU level, the Commission's Digital Omnibus proposal COM(2025) 837 (19 November 2025) would insert GDPR Art. 88a, whose para 3(c) allows storage or access without consent where necessary for "creating aggregated information about the usage of an online service to measure the audience of such a service, where it is carried out by the controller of that online service solely for its own use". This is a proposal with no legal effect. On 2026-11-13 it was still at first reading: Parliament's committees had not voted, and the Council agreed its negotiating mandate on 21 October 2026, in which Art. 88a appears to be deleted and the audience measurement exemption moved to ePrivacy Art. 5(3) (S14). It covers personal data in terminal equipment, not § 25 TDDDG as such.

**Note.** Germany has only a strictly necessary exemption. The DSK gives no blanket answer for reach measurement: it decides case by case, by configuration and purpose.

**Sources.**
- https://www.gesetze-im-internet.de/ttdsg/__25.html
- https://datenschutzkonferenz-online.de/media/oh/OH_Digitale_Dienste.pdf
- https://eur-lex.europa.eu/legal-content/EN/TXT/HTML/?uri=CELEX:52025PC0837

Date checked: 2026-11-12; Omnibus status updated 2026-11-13. Confidence: high on statute and DSK text; high on Omnibus status (see S14). Status: confirmed (statute and DSK position); Omnibus confirmed as a proposal not adopted (S14).

---

<a id="S5"></a>
## S5. Netherlands: Telecommunicatiewet Art. 11.7a

**Supported claim.**
- Art. 11.7a(1) (version in force from 15 August 2026) applies to "het via een elektronisch communicatienetwerk opslaan van of toegang verkrijgen tot informatie in de randapparatuur van een gebruiker", requiring information and consent. The rule is technology-neutral: it is not limited to cookies.
- Art. 11.7a(3)(b) exempts storage or access that is strictly necessary for the requested information society service "of, mits dit geen of geringe gevolgen heeft voor de persoonlijke levenssfeer van de betrokken abonnee of gebruiker, om informatie te verkrijgen over de kwaliteit of effectiviteit van een geleverde dienst van de informatiemaatschappij".
- Art. 11.7a(4): an operation aimed at collecting, combining or analysing data about the user's use of different information society services so the user can be treated differently is presumed to be processing of personal data.
- Autoriteit Persoonsgegevens (English cookies page): "limited analytics cookies" (number of visitors, most visited pages) with no or little impact on privacy need no consent, citing Art. 11.7a; if they process personal data, a legal basis is needed and the use must be stated in the privacy statement.

**Not confirmed.** No AP guidance was found specifically on non-cookie techniques; the statement that the exemption covers cookieless access rests on the statutory wording only (Inference: the exemption follows the same "opslaan van of toegang verkrijgen tot" scope as para 1). The AP page's further conditions (for example on sharing with third parties) were not read in full; one AP URL returned 404.

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

**Note.** The detail above was read through a summarising fetch of the docweb page, not a full manual read of section 7.2; quote it from the source before relying on exact wording.

**Sources.**
- https://www.garanteprivacy.it/home/docweb/-/docweb-display/docweb/9677876

Date checked: 2026-11-12. Confidence: medium. Status: confirmed (conditions), wording to be rechecked against the full text.

---

<a id="S7"></a>
## S7. Turkey: KVKK cookie guide and cross-border transfer rules

**Supported claim.**
- Cookie guide: "Çerez Uygulamaları Hakkında Rehber", KVKK Yayınları No: 69, the edition served on 2026-11-12 is dated "Temmuz 2025, Ankara". Section 4 sets Kriter A (cookie used solely to carry out communication over an electronic communications network) and Kriter B (strictly necessary for an information society service explicitly requested). Section 5 is titled "Açık rıza dışındaki diğer işleme şartları dahilinde çerez kullanım senaryoları" (cookie scenarios under processing conditions other than explicit consent). Section 5.9, "Birinci Taraf Analitik Çerezler (Kriter B)", states that first-party analytics "Kriter B kapsamında değerlendirilebileceği düşünülmektedir", provided they are used only to produce anonymous statistics and not for cross-site tracking; IP masking and not passing data to third parties are recommended. Section 6 (explicit consent scenarios) lists social plug-in tracking cookies and online behavioural advertising cookies.
- Law No. 7499 (accepted 2 March 2024, Resmî Gazete No. 32487, 12 March 2024) amends Art. 9 of Law No. 6698. New Art. 9(5): "Standart sözleşme, imzalanmasından itibaren beş iş günü içinde veri sorumlusu veya veri işleyen tarafından Kuruma bildirilir." Art. 40(1)(a) of Law 7499 puts the relevant articles into force on 1 June 2024.
- "Kişisel Verilerin Yurt Dışına Aktarılmasına İlişkin Usul ve Esaslar Hakkında Yönetmelik", Resmî Gazete 10 July 2024, No. 32598. Art. 14(5): the standard contract is notified to the Authority within five business days of completion of signatures, physically, by KEP, or by other methods the Board sets.
- EU adequacy: the European Commission's list of adequacy decisions (17 entries) does not include Turkey.

**Note.** The current official edition (July 2025) places first-party analytics under Kriter B, outside explicit consent, under conditions (anonymous statistics, no cross-site tracking). The original 2022 edition was not obtained, so this entry does not say whether it differed. Third-party analytics are not addressed as such in section 5.9.

**Sources.**
- https://kvkk.gov.tr/Icerik/7353/Cerez-Uygulamalari-Hakkinda-Rehber
- https://www.resmigazete.gov.tr/eskiler/2024/03/20240312-1.htm
- https://www.resmigazete.gov.tr/eskiler/2024/07/20240710-2.htm
- https://commission.europa.eu/law/law-topic/data-protection/international-dimension-data-protection/adequacy-decisions_en

Date checked: 2026-11-12. Confidence: high (guide text, Law 7499, Regulation, adequacy list). Status: confirmed (July 2025 edition of the cookie guide, transfer rules, no adequacy decision); the 2022 edition of the guide was not obtained.

---

<a id="S8"></a>
## S8. CJEU C-413/23 P, EDPS v SRB

**Supported claim.**
- Judgment of the Court (First Chamber), 4 September 2025.
- Para 86: pseudonymised data must not be regarded as constituting personal data in all cases and for every person, since pseudonymisation may, depending on the circumstances, "effectively prevent persons other than the controller from identifying the data subject".
- Para 77: for a recipient (Deloitte) the data may not be personal where it cannot lift the pseudonymisation measures and cannot re-identify by other means. Para 85: where third parties can re-identify, the data remain personal; the recipient's position does not change the controller's assessment.
- Paras 111 and 112: for the information obligation, identifiability "must be assessed at the time of collection of the data" and from the controller's point of view.
- Operative part: sets aside the General Court judgment of 26 April 2023 (T-557/20), refers the case back to the General Court, reserves costs.

**Inference (not the Court's).** For a salted hash of IP and user agent: while the operator holds or can recompute the salt, the operator can re-identify in the relevant sense, so the hashes are personal data for the operator. Once a random salt is destroyed and cannot be recomputed by anyone, the argument for treating stored hashes as non-personal relative to the operator becomes stronger, but under paras 111 and 112 the controller's obligations at collection time (while the salt existed) still apply.

**Sources.**
- https://eur-lex.europa.eu/legal-content/EN/TXT/HTML/?uri=CELEX:62023CJ0413

Date checked: 2026-11-12. Confidence: high on judgment; inference unreviewed. Status: confirmed.

---

<a id="S9"></a>
## S9. EU-US Data Privacy Framework litigation (Latombe)

**Supported claim.**
- General Court (Tenth Chamber, Extended Composition), judgment of 3 September 2025, T-553/23 Latombe v Commission: "Dismisses the action"; Mr Latombe bears his own costs and the Commission's.
- Appeal C-703/25 P: "Appeal brought on 31 October 2025 by Philippe Latombe", published in OJ C on 22 December 2025 (C/2025/6610). Pleas concern the independence and establishment by law of the Data Protection Review Court, bulk collection, and presidential power under Executive Order 14086.
- InfoCuria on 2026-11-12 shows the case as "Pending" before the Court of Justice, with an Order of 4 June 2026 (ECLI:EU:C:2026:465). The order's text could not be loaded (EUR-Lex returned "document does not exist"), so its subject is unknown. No judgment is listed.

**Sources.**
- https://eur-lex.europa.eu/legal-content/EN/TXT/HTML/?uri=CELEX:62023TJ0553
- https://eur-lex.europa.eu/eli/C/2025/6610/oj/eng
- https://infocuria.curia.europa.eu/tabs/redirect/juris/liste.jsf?num=C-703/25&language=en

Date checked: 2026-11-12. Confidence: high. Status: confirmed (order content unconfirmed).

---

<a id="S10"></a>
## S10. GDPR Art. 11 and Art. 28 (Regulation (EU) 2016/679)

**Supported claim.**
- Art. 11(1): "If the purposes for which a controller processes personal data do not or do no longer require the identification of a data subject by the controller, the controller shall not be obliged to maintain, acquire or process additional information in order to identify the data subject for the sole purpose of complying with this Regulation."
- Art. 11(2): where the controller can demonstrate it cannot identify the data subject, it informs the data subject if possible, and Arts. 15 to 20 do not apply unless the data subject provides additional information enabling identification.
- Art. 28(3): the contract must set out subject-matter and duration, nature and purpose, type of personal data, categories of data subjects, and the controller's obligations and rights, and must stipulate that the processor: (a) processes only on documented instructions, including on third-country transfers; (b) ensures confidentiality of authorised persons; (c) takes all measures required by Art. 32; (d) respects the conditions of paras 2 and 4 for sub-processors; (e) assists with data subject requests; (f) assists with Arts. 32 to 36; (g) at the controller's choice deletes or returns all personal data at the end of services; (h) makes information available and allows for and contributes to audits. The processor must immediately inform the controller if an instruction infringes data protection law. Art. 28(2) and (4) govern sub-processor authorisation and flow-down.

**Sources.**
- https://eur-lex.europa.eu/legal-content/EN/TXT/HTML/?uri=CELEX:32016R0679

Date checked: 2026-11-12. Confidence: high. Status: confirmed.

---

<a id="S12"></a>
## S12. United States (California): CCPA as amended by the CPRA, and CPPA regulations

**Supported claim.**
- The Act has no consent requirement for collection or for analytics. The consumer choices are an opt-out of sale and sharing and a limit on sensitive personal information. § 1798.120(a)(1): "A consumer shall have the right, at any time, to direct a business that sells or shares personal information about the consumer to third parties not to sell or share".
- Opt-in exists only for minors, and only for sale or sharing. § 1798.120(c): "a business shall not sell or share the personal information of consumers if the business has actual knowledge that the consumer is less than 16 years of age, unless ... [the consumer or parent] has affirmatively authorized the sale or sharing".
- Analytics by a service provider is a business purpose. § 1798.140(e)(5): "Performing services on behalf of the business, including ... providing analytic services, providing storage, or providing similar services on behalf of the business."
- A service provider is not a third party (§ 1798.140(ai)(2): "A service provider to the business"). "Sell" (ad)(1) and "share" (ah)(1) both require disclosure "to a third party"; "share" also requires "cross-context behavioral advertising" (k): "the targeting of advertising to a consumer based on the consumer's personal information obtained from the consumer's activity across businesses, distinctly branded internet websites, applications, or services".
- Service provider (§ 1798.140(ag)(1)): a person that processes personal information on behalf of a business "pursuant to a written contract" that prohibits it from "(A) Selling or sharing", "(B) Retaining, using, or disclosing ... for any purpose other than for the business purposes specified in the contract", "(C) ... outside of the direct business relationship", and "(D) Combining the personal information ... with personal information that it receives from ... another person or persons, or collects from its own interaction with the consumer".
- Notice at collection (§ 1798.100(a)): a business "shall, at or before the point of collection, inform consumers of ... (1) The categories of personal information to be collected and the purposes ... and whether that information is sold or shared ... (3) The length of time the business intends to retain each category". § 1798.100(c): collection must be "reasonably necessary and proportionate". § 1798.100(d) lists the terms of the agreement with a service provider.
- Personal information (§ 1798.140(v)(1)) includes identifiers such as "online identifier, Internet Protocol address" and "Internet or other electronic network activity information". (v)(3): it "does not include consumer information that is deidentified or aggregate consumer information". § 1798.145(a)(1): "The obligations imposed on businesses by this title shall not restrict a business's ability to: ... (F) Collect, use, retain, sell, share, or disclose consumers' personal information that is deidentified or aggregate consumer information."
- Business thresholds (§ 1798.140(d)(1)): annual gross revenues above $25,000,000 "as adjusted" (the CPPA's adjustment page gives $26,625,000 from 1 January 2025), or buying, selling or sharing the personal information of 100,000 or more consumers or households, or 50 percent or more of revenue from selling or sharing.
- § 1798.135(a) (the "Do Not Sell or Share" link) applies to "A business that sells or shares consumers' personal information".
- Regulations (Cal. Code Regs. tit. 11, approved text effective 1 January 2026):
  - § 7012(d): "If a business does not give the Notice at Collection to the consumer at or before the point of collection of their personal information, the business shall not collect personal information from the consumer." § 7012(c) allows a conspicuous link on the introductory page and on every page where personal information is collected.
  - § 7012(h): "A business that neither collects nor controls the collection of personal information directly from the consumer does not need to provide a Notice at Collection to the consumer if it neither sells nor shares the consumer's personal information."
  - § 7025(c)(1) on opt-out preference signals (such as GPC): "This is not required for a business that does not sell or share personal information."
  - § 7050(e): a person without a contract complying with § 7051(a) "is not a service provider", and disclosure to it "may be considered a sale or sharing of personal information". § 7051(a) requires, among other terms, that the contract "Identify the specific business purpose(s)", which "shall not be described in generic terms".
  - § 7050(b): "A service provider or contractor cannot contract with a business to provide cross-context behavioral advertising."

**Inference.** No opt-in is needed for this service in California. The conditions are: the tenant is a "business" under § 1798.140(d) (if not, the Act does not apply to it); the tenant gives a notice at collection; the tenant and the operator sign a contract meeting § 1798.140(ag), § 1798.100(d) and § 7051(a); and the operator does not combine data across tenants or use it for its own purposes. Honouring GPC is not required where nothing is sold or shared, but it does no harm. Whether a daily, per-site visitor hash is a "unique identifier" ((aj) requires a "persistent identifier ... over time and across different services") is an inference; this register treats the raw events as personal information.

**Not read.** § 1798.130 (privacy policy contents), regulations §§ 7004 and 7011. The regulations were read in the strike-and-underline amendment text, so the exact wording of § 7050(a) and the numbering of § 7051 are not yet checked against a clean copy.

**Sources.**
- https://leginfo.legislature.ca.gov/faces/codes_displaySection.xhtml?lawCode=CIV&sectionNum=1798.100 (and the same URL with sectionNum 1798.120, 1798.121, 1798.135, 1798.140, 1798.145). Read through a rendering scraper; a plain HTTP client still gets HTTP 403.
- https://cppa.ca.gov/regulations/cpi_adjustment.html
- https://cppa.ca.gov/regulations/ccpa_updates.html
- https://cppa.ca.gov/regulations/pdf/ccpa_updates_cyber_risk_admt_appr_text.pdf

Date checked: 2026-11-13. Confidence: high (statute), medium (regulation wording). Status: confirmed (statute); partly confirmed (regulations, read in amendment form).

---

<a id="S13"></a>
## S13. Brazil: LGPD (Lei 13.709/2018) and ANPD guidance

**Supported claim.**
- Art. 7, IX: processing is allowed "quando necessário para atender aos interesses legítimos do controlador ou de terceiro, exceto no caso de prevalecerem direitos e liberdades fundamentais do titular que exijam a proteção dos dados pessoais". Art. 7 § 6: not needing consent "não desobriga os agentes de tratamento das demais obrigações previstas nesta Lei".
- Art. 10 caput: legitimate interest may support processing only "para finalidades legítimas, consideradas a partir de situações concretas", including "apoio e promoção de atividades do controlador", "respeitadas as legítimas expectativas" of the data subject. § 1: "somente os dados pessoais estritamente necessários para a finalidade pretendida poderão ser tratados". § 2: "O controlador deverá adotar medidas para garantir a transparência do tratamento de dados baseado em seu legítimo interesse." § 3: the ANPD may ask the controller for a data protection impact report (relatório de impacto).
- Art. 12 caput: "Os dados anonimizados não serão considerados dados pessoais para os fins desta Lei, salvo quando o processo de anonimização ao qual foram submetidos for revertido ... ou quando, com esforços razoáveis, puder ser revertido." § 1 sets cost, time and available technology as the test of what is reasonable.
- Art. 18 § 2: the data subject "pode opor-se a tratamento realizado com fundamento em uma das hipóteses de dispensa de consentimento, em caso de descumprimento ao disposto nesta Lei". Art. 39: the processor (operador) acts "segundo as instruções fornecidas pelo controlador".
- ANPD, "Guia Orientativo: Cookies e Proteção de Dados Pessoais", Versão 1.0, October 2022. Scope (p. 6) includes "tecnologias similares de rastreamento". Page 24: "A utilização de cookies para fins de medição de audiência (cookies analíticos ou de medição) pode ser amparada na hipótese legal do legítimo interesse em determinados contextos ... é razoável supor que a medição de audiência constituirá um interesse legítimo do controlador, bem como que os riscos à privacidade de titulares serão de menor monta quando o tratamento se limitar à finalidade específica de identificação de padrões e tendências, com base em dados agregados e sem a combinação com outros mecanismos de rastreamento ou sem a formação de perfis de usuários." Page 23: the controller must assess, before processing, whether the data subject's rights prevail. Page 15 and page 34: browser settings do not replace "um mecanismo direto e próprio" for exercising rights, including objection.
- ANPD, "Guia Orientativo: Hipóteses legais de tratamento de dados pessoais: Legítimo Interesse", February 2024. Page 29: legitimate interest "deve ser precedido de um teste de balanceamento", applied "para cada finalidade específica". Page 30: the test "pode ser breve ou simplificado" where low impact is clearly identified.

**Inference.** Consent is not required. The basis is the tenant's (controller's) legitimate interest. The tenant documents a short balancing test, state the processing in its privacy notice, and offer a direct opt-out link. Art. 12 helps for stored aggregates but does not replace a legal basis, because collection and the daily hash are processing of personal data before aggregation.

**Not read on planalto.gov.br.** Every Planalto URL (l13709.htm, L13709compilado.htm, http and https) redirected to the gov.br home page on 2026-11-13. The text was read on the Câmara dos Deputados compiled version and checked against the Senado amendment register, which shows no amendment to Arts. 10 and 12 and none to Art. 7 IX.

**Sources.**
- https://www2.camara.leg.br/legin/fed/lei/2018/lei-13709-14-agosto-2018-787077-normaatualizada-pl.html
- https://legis.senado.leg.br/norma/27457334
- https://www.gov.br/anpd/pt-br/centrais-de-conteudo/materiais-educativos-e-publicacoes/guia-orientativo-cookies-e-protecao-de-dados-pessoais.pdf/@@display-file/file
- https://www.gov.br/anpd/pt-br/centrais-de-conteudo/materiais-educativos-e-publicacoes/guia_legitimo_interesse.pdf/@@display-file/file

Date checked: 2026-11-13. Confidence: high (statute and guide text), medium-high (application). Status: confirmed (statute via Câmara and Senado, ANPD guides); the Planalto copy remained unreachable.

---

<a id="S14"></a>
## S14. EU Digital Omnibus, COM(2025) 837 (procedure 2025/0360(COD))

**Supported claim.**
- Commission proposal of 19 November 2025, registered by the Council as ST 15698/25 on 20 November 2025. Its proposed GDPR Art. 88a(3)(c) allows storage or access without consent where necessary for "creating aggregated information about the usage of an online service to measure the audience of such a service, where it is carried out by the controller of that online service solely for its own use". The proposal also adds to ePrivacy Art. 5(3): "This paragraph shall not apply if the subscriber or user is a natural person, and the information stored or accessed constitutes or leads to the processing of personal data."
- Parliament (OEIL file 2025/0360(COD), log to 12 November 2026): referral to ITRE and LIBE jointly announced 19 January 2026; draft report PE786.818 of 22 June 2026; amendments tabled 27 July 2026. "Stage reached in procedure": "Awaiting committee decision". No committee vote, plenary position, trilogue agreement or adoption is recorded.
- Council: Coreper agreed the mandate for negotiations with Parliament on 21 October 2026 (ST 14756/26, "Outcome of proceedings", 22 October 2026). In that mandate the GDPR lead-in reads "the following ~~articles are~~ **article is** added", and the Art. 88a block is struck through. The terminal-equipment rules return to ePrivacy Art. 5(3) with a closed list of exemptions. Point (c), as extracted: "Creating anonymous aggregated information about the usage of an online service to measure the audience of that service, provided that it is carried out by the provider of the service requested by the subscriber or user, or by a third party on behalf of that provider, and the data collected for the purpose of aggregating ... are not processed for another purpose, shared with third parties or combined with data from third parties". The mandate gives Member States 24 months to transpose.
- The separate AI omnibus, COM(2025) 836 (2025/0359(COD)), was adopted as Regulation (EU) 2026/1744 (OJ L, 24 July 2026). It amends the AI Act only and does not contain Art. 88a.

**Status in plain words.** As of 2026-11-13 COM(2025) 837 is a proposal at first reading. It has no legal effect. National Art. 5(3) laws apply unchanged. The Council's text is a negotiating position, not law.

**Not confirmed.** That the Council deleted Art. 88a is read from strike-through marks in the PDF, not from a clean consolidated text (medium-high confidence). The quoted point (c) merges struck and new words in extraction; check it against the PDF. The Council press release of 21 October 2026 returned HTTP 403 and was not read. The EUR-Lex copy of the proposal did not load on 2026-11-13; the Council register copy was used.

**Sources.**
- https://oeil.europarl.europa.eu/oeil/en/procedure-file/pdf?reference=2025%2F0360%28COD%29
- https://data.consilium.europa.eu/doc/document/ST-15698-2025-INIT/en/pdf
- https://data.consilium.europa.eu/doc/document/ST-14756-2026-INIT/en/pdf
- https://oeil.europarl.europa.eu/oeil/en/procedure-file/pdf?reference=2025%2F0359%28COD%29
- https://eur-lex.europa.eu/legal-content/EN/TXT/HTML/?uri=CELEX:52025PC0837 (read on 2026-11-12, failed to load on 2026-11-13)

Date checked: 2026-11-13. Confidence: high (stages, non-adoption, proposal text), medium-high (content of the Council mandate). Status: confirmed (not adopted, latest stage Council mandate of 21 October 2026, Parliament committee vote pending).

---

<a id="S15"></a>
## S15. Austria: TKG 2021 § 165(3) and DSB FAQ

**Supported claim.**
- Telekommunikationsgesetz 2021 (BGBl. I Nr. 190/2021) § 165(3): "Eine Ermittlung dieser Daten ist nur zulässig, wenn der Nutzer oder Benutzer seine Einwilligung dazu aktiv und auf Grundlage von klaren und umfassenden Informationen erteilt hat." Exemption: "Dies steht einer technischen Speicherung oder dem Zugang nicht entgegen, wenn der alleinige Zweck die Durchführung der Übertragung einer Nachricht über ein Kommunikationsnetz ist oder, wenn dies unbedingt erforderlich ist, damit der Anbieter eines Dienstes der Informationsgesellschaft, der vom Nutzer oder Benutzer ausdrücklich gewünscht wurde, diesen Dienst zur Verfügung stellen kann." No analytics exemption.
- Datenschutzbehörde FAQ "Datenschutz und Cookies" (undated): "Aus technischer Sicht nicht notwendig (und daher einwilligungsbedürftig) sind insbesondere jene Dienste, die das Nutzerverhalten von Personen auf der jeweiligen Website oder über mehrere Websites oder Endgeräte aufzeichnen und auswerten." Its list of technically necessary uses includes "Die Information über den Einwilligungsstatus, sofern hierfür keine eindeutige Online-Kennung vergeben wird". It adds that enforcing § 165(3) "obliegt ... den Fernmeldebehörden".

**Not checked.** Guidance of the telecom authorities (Fernmeldebehörden). The main RIS page served a captcha; the text was read through the official RIS open data API.

**Sources.**
- https://www.ris.bka.gv.at/eli/bgbl/i/2021/190/P165/NOR40238623
- https://ogd.ris.bka.gv.at/Dokumente/Bundesnormen/NOR40238623/NOR40238623.html
- https://dsb.gv.at/faqs/datenschutz-cookies

Date checked: 2026-11-13. Confidence: medium-high. Status: confirmed (statute and DSB FAQ); telecom authority guidance not checked.

---

<a id="S16"></a>
## S16. Belgium: Law of 30 July 2018, Art. 10/2, and APD/GBA guidance

**Supported claim.**
- The rule formerly in Art. 129 of the Wet elektronische communicatie is now Art. 10/2 of the Loi du 30 juillet 2018 (inserted by the law of 21 December 2021, Art. 256, in force 10 January 2022). Storage or access "est autorisée uniquement à condition que" the user receives clear and precise information and "ait donné son consentement". Exemption: "L'alinéa 1er ne s'applique pas à l'enregistrement technique des informations ou de l'accès ... ayant pour seul but de réaliser l'envoi d'une communication via un réseau de communications électroniques ou de fournir un service demandé expressément par l'abonné ou l'utilisateur final lorsque cela est strictement nécessaire à cet effet."
- APD/GBA cookie checklist, footnote 12, on the strictly necessary exemption: "En principe, ceci n'inclut dès lors pas le comptage des visiteurs". It notes that "Quelques contrôleurs (dans d'autres États membres)" exempt such cookies under strict conditions; the APD does not adopt that position.
- APD/GBA citizen page on cookies: "Un cookie pour des finalités analytiques ne répond en principe donc pas à ces deux conditions. Plus précisément, ce cookie n'est pas nécessaire pour fournir un service demandé par l'utilisateur, mais uniquement pour communiquer des données statistiques au fournisseur du site Internet." The page says the rules also cover "le stockage local ou local storage".

**Sources.**
- https://www.ejustice.just.fgov.be/cgi_loi/change_lg.pl?language=fr&la=F&cn=2018073046&table_name=loi
- https://www.autoriteprotectiondonnees.be/publications/checklist-cookies.pdf
- https://www.autoriteprotectiondonnees.be/citoyen/themes/internet/cookies

Date checked: 2026-11-13. Confidence: high. Status: confirmed.

---

<a id="S17"></a>
## S17. Bulgaria: E-Commerce Act Art. 4a and CPDP guidance

**Supported claim.**
- The cookie rule is in the Закон за електронната търговия (ЗЕТ), чл. 4а, not in the Electronic Communications Act (whose чл. 4а concerns spectrum). ЗЕТ чл. 4а ал. 1: the provider "съхранява информация или получава достъп до информация, съхранена в крайното устройство на получателя на услугата, при условие че: 1. [...] е предоставена ясна и изчерпателна информация по чл. 13 от Регламент (ЕС) 2016/679 [...]; 2. на получателя на услуга на информационното общество е предоставена възможност да откаже съхраняването или достъпа до информацията." (Information plus a possibility to refuse: the text reads as opt-out.) Ал. 4 exempts storage or access "необходими за: 1. предаването на съобщения по електронна съобщителна мрежа; 2. предоставяне на услуга на информационното общество, изрично поискана от получателя".
- КЗЛД (CPDP), "Специфични насоки за различни сектори", section on cookies (undated): "Статистически (analytical) [...] Изискват съгласие, освен ако се използват в анонимизиран вид и не се споделят." (Statistics cookies need consent unless used in anonymised form and not shared.) The same page also describes the law as requiring prior consent unless a cookie is strictly necessary, which is stricter than the statutory wording.

**Not confirmed.** The statute was read in the consolidated text published by the Ministry of Transport and Communications (last amendment listed ДВ бр. 99 of 21 November 2025); it was not checked on parliament.bg or the State Gazette. The CPDP does not define "анонимизиран" for this purpose.

**Sources.**
- https://www.mtc.government.bg/sites/default/files/documents/2026-01/Закон%20за%20електронната%20търговия.pdf
- https://cpdp.bg/специфични-насоки-за-различни-сектор/

Date checked: 2026-11-13. Confidence: medium. Status: partly confirmed (statute and CPDP text read; the two are inconsistent, and the anonymity condition is undefined).

---

<a id="S18"></a>
## S18. Croatia: ZEK Art. 43(4) and AZOP guidance

**Supported claim.**
- Zakon o elektroničkim komunikacijama (NN 76/22) Art. 43(4): storage or access "dopušteno je samo u slučaju kada je taj krajnji korisnik ili korisnik dao svoju privolu, nakon što je dobio jasnu i potpunu obavijest". Exemption: "Time se ne može spriječiti tehnička pohrana podataka ili pristup podacima isključivo u svrhu obavljanja prijenosa komunikacija putem elektroničke komunikacijske mreže, ili, ako je to nužno, radi pružanja usluga informacijskog društva na izričit zahtjev krajnjeg korisnika ili korisnika." The amending act NN 45/26 does not change Art. 43.
- AZOP answer published 14 August 2026: "Ako se, međutim, koriste analitički, marketinški ili drugi kolačići koji nisu tehnički nužni, isti se ne smiju postaviti niti aktivirati prije nego što korisnik za njih dade valjanu privolu."
- AZOP page on cookies lists the cookies that can be exempt, following WP29; analytics is not among them.

**Not read.** NN 14/24 itself (an unofficial consolidation shows Art. 43(4) unchanged).

**Sources.**
- https://narodne-novine.nn.hr/clanci/sluzbeni/2022_07_76_1116.html
- https://narodne-novine.nn.hr/clanci/sluzbeni/2026_04_45_523.html
- https://azop.hr/kolacici-gdpr/
- https://azop.hr/obrada-osobnih-podataka-kolacici/

Date checked: 2026-11-13. Confidence: high. Status: confirmed.

---

<a id="S19"></a>
## S19. Cyprus: Law 112(I)/2004 Art. 99(5) and Commissioner guidance

**Supported claim.**
- Ν. 112(Ι)/2004 άρθρο 99(5): storage or access "επιτρέπεται μόνον εάν ο συγκεκριμένος συνδρομητής ή χρήστης έχει δώσει τη συγκατάθεσή του". Exemption for transmission or where "απολύτως αναγκαία" for a service the user explicitly requested.
- Commissioner for Personal Data Protection, "Cookies", published 13 October 2025: "Τα cookies τα οποία παρέχουν στατιστικά δεδομένα σχετικά με την επισκεψιμότητα της σελίδας (analytics) ... δεν εξαιρούνται από την υποχρέωση λήψης συγκατάθεσης καθώς αυτά δεν «απολύτως απαραίτητα», αφού ο χρήστης μπορεί να λάβει όλες τις υπηρεσίες της ιστοσελίδας και χωρίς αυτά." (Analytics cookies are not exempt from consent.) The page confirms the rule is in Art. 99(5).

**Caveat.** The statute was read on CyLaw, run by the Cyprus Bar Association, not a government portal. The Commissioner's page confirms the article number and the rule.

**Sources.**
- https://www.cylaw.org/nomoi/enop/non-ind/2004_1_112/full.html
- https://www.gov.cy/dataprotection/documents/cookies/

Date checked: 2026-11-13. Confidence: high. Status: confirmed (rule via the Commissioner; statute text via CyLaw).

---

<a id="S20"></a>
## S20. Czechia: Act 127/2005 § 89(3) and ÚOOÚ guidance

**Supported claim.**
- Zákon 127/2005 Sb. § 89 odst. 3 (current version from 1 November 2026, 197/2026 Sb.): anyone storing or accessing data in terminal equipment "získá od těchto účastníků nebo uživatelů předem prokazatelný souhlas s rozsahem a účelem jejich zpracování". Exemption: "technické ukládání nebo přístup výhradně pro potřeby přenosu zprávy [...] nebo je-li to nezbytné pro potřeby poskytování služby informační společnosti, která je výslovně vyžádána účastníkem nebo uživatelem."
- ÚOOÚ Q&A on cookies: cookies "určené k sledování návštěvnosti, analýze preferencí" are "netechnické cookies", which may be stored or accessed "pouze na základě souhlasu uživatele". "Tyto stanovené podmínky platí i pro další formy ukládání dat [...] (technologie podobné cookies jako objekty v místním úložišti [...])". ÚOOÚ seminar of 12 June 2024: collection "pro statistické, analytické, marketingové či jiné účely = netechnické cookies"; since 1 January 2022 the rule is "opt-in".

**Sources.**
- https://www.e-sbirka.cz/sb/2005/127?zalozka=text
- https://uoou.gov.cz/verejnost/qa-otazky-a-odpovedi/cookies
- https://uoou.gov.cz/media/seminare-uoou/prezentace/2024-06-12-p04-praxe-uoou-v-oblasti-cookies.pdf

Date checked: 2026-11-13. Confidence: high. Status: confirmed.

---

<a id="S21"></a>
## S21. Denmark: cookiebekendtgørelsen and joint DIGST/Datatilsynet guidance

**Supported claim.**
- BEK nr 1148 af 09/12/2011 (in force) § 3(1): no storage or access "hvis slutbrugeren ikke giver samtykke hertil efter at have modtaget fyldestgørende information". § 4(1): exemptions for transmission only, or where storage or access "er påkrævet for at sætte tjenesteyderen af en informationssamfundstjeneste, som slutbrugeren udtrykkelig har anmodet om, i stand til at levere denne tjeneste". § 4(2): "påkrævet" means "en teknisk forudsætning for at kunne levere en tjeneste".
- Joint guidance of Digitaliseringsstyrelsen and Datatilsynet (2025): "Du skal derfor indhente samtykke, hvis du bruger cookies og lignende teknologier til f.eks. generere besøgsstatistik, eller vise personaliserede annoncer." DIGST: the rules cover more than cookies, pixels and fingerprints ("Cookies, pixels eller fingerprints er ikke udtømmende").

**Sources.**
- https://www.retsinformation.dk/eli/lta/2011/1148
- https://digst.dk/tilsyn/sporingsteknologiomraadet/cookievejledningen/
- https://www.datatilsynet.dk/Media/638829899950476628/Fællesvejledning%20med%20DIGST%20-%20Cookie%20og%20lignende%20teknologier.pdf

Date checked: 2026-11-13. Confidence: high. Status: confirmed.

---

<a id="S22"></a>
## S22. Estonia: ESS § 102 and AKI guidance

**Supported claim.**
- Elektroonilise side seadus, version in force from 1 November 2026 (RT I, 11.07.2026, 86). There is no § 102² in the current text. § 102(3) allows the processing in subsection 1 if the undertaking "teavitab klienti selgelt ja arusaadavalt andmete töötlemise eesmärkidest ning annab kliendile võimaluse keelduda". § 102(4) allows processing without consent where the sole purpose is transmission or where "see on vajalik kliendi poolt otseselt taotletud infoühiskonna teenuse osutamiseks". The provision is addressed to the "sideettevõtja" (communications undertaking).
- Andmekaitse Inspektsioon FAQ: "Analüütilised küpsised, reklaamküpsised või autentimise küpsised, mis võimaldavad inimest kas otseselt või kaudselt tuvastada - seda tüüpi küpsiste paigaldamiseks peab olema kasutaja eelnev vabatahtlik nõusolek." It grounds this on Art. 5(3) of Directive 2002/58/EC. The same sentence appears in "Andmeturbe soovitused e-poodidele" v4.0 (17 April 2025), which adds that pixels are access to information stored in the terminal.

**Not confirmed.** How ESS § 102, written for communications undertakings and with an opt-out wording in subsection 3, maps onto Art. 5(3) for website operators. AKI relies on the Directive, not on § 102. AKI's rule is phrased for analytics that allow direct or indirect identification; it states no exemption for analytics that do not.

**Sources.**
- https://www.riigiteataja.ee/akt/ESS
- https://www.aki.ee/isikuandmed/kkk/interneti-ja-veebitegevused
- https://www.aki.ee/sites/default/files/documents/2025-04/Andmeturbe%20soovitused%20e-poodidele_v%204.0.pdf

Date checked: 2026-11-13. Confidence: medium. Status: partly confirmed (AKI position confirmed; statutory mapping unclear).

---

<a id="S23"></a>
## S23. Finland: Act on Electronic Communications Services 917/2014 § 205 and Traficom guidance

**Supported claim.**
- Laki sähköisen viestinnän palveluista 917/2014, § 205 (consolidated to 16 June 2026). Subsection 1: storing and using "evästeiden tai muiden palvelun käyttöä kuvaavien tietojen" is allowed "jos käyttäjä on antanut siihen suostumuksensa". Subsection 2 exempts transmission and use "joka on välttämätöntä palvelun tarjoajalle sellaisen palvelun tarjoamiseksi, jota tilaaja tai palvelun käyttäjä on nimenomaisesti pyytänyt". Subsection 3: only "palvelun vaatimassa laajuudessa".
- Traficom guide for service providers (TRAFICOM/295680/04.04.05.03/2021, 1 September 2021, linked from the Kyberturvallisuuskeskus page updated 12 October 2026): "Analytiikkaevästeitä ei kuitenkaan voida yksiselitteisesti katsoa käyttäjän näkökulmasta välttämättömiksi". "Mikäli analytiikkaevästeet halutaan katsoa juuri kyseessä olevan palvelun tarjoamisen kannalta ehdottoman välttämättömiksi, palveluntarjoajan tulee voida esittää menettelylle selkeät perusteet ja huolehtia käyttäjän yksityisyyden suojaamisesta esimerkiksi varmistamalla, ettei analytiikan avulla kerättyjä tietoja jaeta kolmansille osapuolille tai että niistä ole tunnistettavissa yksittäistä kävijää." Otherwise "tulisi suostumusta analytiikan käyttöön pyytää".

**Sources.**
- https://www.finlex.fi/fi/lainsaadanto/2014/917
- https://www.traficom.fi/files/media/file/Evästeohjeistus_palveluntarjoajille.pdf
- https://www.kyberturvallisuuskeskus.fi/fi/viestintaverkkojen-ja-palveluiden-saantely/evasteet

Date checked: 2026-11-13. Confidence: medium. Status: confirmed.

---

<a id="S24"></a>
## S24. Greece: Law 3471/2006 Art. 4(5) and Hellenic DPA Recommendations 1/2020

**Supported claim.**
- Ν. 3471/2006 Art. 4(5), as replaced by Art. 170 of Ν. 4070/2012: storage or access "επιτρέπεται μόνο αν ο συγκεκριμένος συνδρομητής ή χρήστης έχει δώσει τη συγκατάθεσή του μετά από σαφή και εκτενή ενημέρωση". Exemption for transmission or storage or access "η οποία είναι αναγκαία για την παροχή υπηρεσίας της κοινωνίας της πληροφορίας, την οποία έχει ζητήσει ρητά ο χρήστης ή ο συνδρομητής".
- Hellenic DPA Recommendations 1/2020 (25 February 2020): scope includes "τοπικό αποθηκευτικό χώρο (local storage)" and fingerprinting. A.1: "Για την τοποθέτηση ενός ιχνηλάτη σε τερματική συσκευή απαιτείται κατ' αρχήν η συγκατάθεση του χρήστη, ανεξάρτητα αν μέσω αυτού πραγματοποιείται τελικά επεξεργασία προσωπικών δεδομένων." A.2 lists technically necessary trackers; analytics is not among them. A.4: "Η χρήση ιχνηλατών τρίτων, όπως η υπηρεσία Google Analytics με σκοπό την στατιστική ανάλυση (web analytics), μπορεί να γίνει μόνο κατόπιν συγκατάθεσης του χρήστη".

**Not confirmed.** The statute was read in the DPA's 2019 consolidation; later amendments in the Government Gazette were not checked. A.4 is worded for third-party trackers; the DPA does not address first-party or processor-run aggregate analytics expressly.

**Sources.**
- https://dpa.gr/sites/default/files/2019-09/%CE%9D3471_06.PDF
- https://www.dpa.gr/sites/default/files/2020-12/Sistaseis_1_2020.pdf
- https://www.dpa.gr/en/by-design/online-marketing-and-advertising-cookies-and-trackers

Date checked: 2026-11-13. Confidence: medium. Status: partly confirmed (statute current to 2019 only).

---

<a id="S25"></a>
## S25. Hungary: Eht. § 155(4) and NAIH guidance

**Supported claim.**
- 2003. évi C. törvény (Eht.) 155. § (4) (version in force 1 October to 19 November 2026; set by 2011. évi CVII. törvény 50. §): "Egy előfizetőnek vagy felhasználónak elektronikus hírközlő végberendezésén csak az érintett felhasználó vagy előfizető világos és teljes körű [...] tájékoztatását követő hozzájárulása alapján lehet adatot tárolni, vagy az ott tárolt adathoz hozzáférni." (Storage or access only with consent after clear and full information.) The paragraph itself contains no exemption.
- NAIH guidance on web shops (NAIH/2017/1060/V), section 3.1: the Authority follows WP29 Opinion 4/2012; consent is not needed for user-input, authentication, user-centric security, multimedia player, load-balancing and UI customisation cookies. Analytics is not on the list.

**Not confirmed.** Where, if anywhere, Hungary transposes the two Art. 5(3) exemptions (not found in § 155). njt.hu reset connections; the text was read on the official mirror njt.jog.gov.hu. NAIH guidance is from 2017 and incorporates WP194 by reference only.

**Sources.**
- https://njt.jog.gov.hu/jogszabaly/2003-100-00-00
- https://naih.hu/files/2017-02-17-webaruhaz-tajekoztato-NAIH-2017-1060-V.pdf

Date checked: 2026-11-13. Confidence: medium. Status: partly confirmed.

---

<a id="S26"></a>
## S26. Iceland: Electronic Communications Act 70/2022 Art. 88(2) and Fjarskiptastofa decision 14/2024

**Supported claim.**
- Lög um fjarskipti nr. 70/2022, 88. gr. 2. mgr., as enacted (Stjórnartíðindi A, 12 July 2022): use of systems that store or access information in the user's terminal "er óheimil nema samkvæmt upplýstu samþykki notanda eða samkvæmt heimild í lögum. Þrátt fyrir þetta er notkun slíks búnaðar heimil til að fá aðgang að upplýsingum og/eða til tæknilegrar geymslu í lögmætum tilgangi og með vitund hlutaðeigandi notanda."
- Fjarskiptastofa decision 14/2024 (3 December 2024) quotes the same wording and reads it narrowly: "Lögmætur tilgangur skal túlkaður þröngt og getur eingöngu átt við í tveimur tilfellum", namely transmission and where use is a necessary precondition for a service the user specifically requested. The decision treats statistics cookies as a separate category from strictly necessary ones; it was dismissed on jurisdiction and made no ruling on analytics.

**Not confirmed.** The current consolidated text on althingi.is returned a bot challenge (HTTP 403), so amendments after December 2024 are not ruled out. No Persónuvernd guidance on analytics was found.

**Sources.**
- https://island.is/stjornartidindi/nr/071f9e66-7028-4720-85c3-9e6293658c98
- https://assets.ctfassets.net/8k0h54kbe6bj/file-080af05e-db5d-4ab8-9208-caa6e290cf2c/d4f588a8ab5a3483fe1a77c5d586894e/080af05e-db5d-4ab8-9208-caa6e290cf2c.pdf (decision PDF linked from island.is)
- https://island.is/frett/kvortun-um-notkun-a-vefkokum-a-vef-ruv-ohf-visad-fra
- https://www.althingi.is/lagas/nuna/2022070.html (failed: HTTP 403)

Date checked: 2026-11-13. Confidence: medium. Status: partly confirmed (enacted text and regulator reading confirmed; current consolidation not read).

---

<a id="S27"></a>
## S27. Ireland: S.I. 336/2011 reg. 5 and DPC guidance

**Supported claim.**
- S.I. No. 336/2011 reg. 5(3): "A person shall not use an electronic communications network to store information, or to gain access to information already stored in the terminal equipment of a subscriber or user, unless (a) the subscriber or user has given his or her consent to that use". Reg. 5(5) exempts transmission and storage or access "strictly necessary in order to provide an information society service explicitly requested by the subscriber or user".
- DPC "Guidance note: Cookies and other tracking technologies" (April 2020): "Do analytics cookies require consent? Yes." Also: "It is unlikely that first-party analytics cookies would be considered a priority for enforcement action by the DPC", where they are "strictly limited to first-party aggregated statistical purposes", disclosed in the privacy policy, with "a user-friendly mechanism to opt out". The note covers local storage and other tracking technologies.
- DPC FAQ "Do I need consent for analytics cookies?": "Yes."

**Note.** The enforcement-priority sentence is discretion, not an exemption.

**Not confirmed.** The regulation was read as made; the amendment index did not load, so later amendments are not ruled out.

**Sources.**
- https://www.irishstatutebook.ie/eli/2011/si/336/made/en/print
- https://www.dataprotection.ie/sites/default/files/uploads/2020-04/Guidance%20note%20on%20cookies%20and%20other%20tracking%20technologies.pdf
- https://dataprotection.ie/en/faqs/cookies/do-i-need-consent-analytics-cookies

Date checked: 2026-11-13. Confidence: high. Status: confirmed (amendment status unverified).

---

<a id="S28"></a>
## S28. Latvia: Information Society Services Law Art. 7.1 and DVI guidance

**Supported claim.**
- Informācijas sabiedrības pakalpojumu likums, 7.¹ pants (version in force 21 June 2024 to 30 November 2026; unchanged in the version from 1 December 2026). (1): storage or access "ir atļauta, ja attiecīgais abonents vai lietotājs ir devis savu piekrišanu". (2): consent is not needed where storage or access "ir nepieciešama informācijas aprites nodrošināšanai elektronisko sakaru tīklā vai starpniecības pakalpojumu sniedzējam, lai sniegtu abonenta vai lietotāja pieprasīto pakalpojumu".
- DVI (22 April 2022): "Piekrišana nav prasāma tikai par tehniskajām sīkdatnēm, bez kuru darbības tīmekļa vietne darbosies nepilnīgi/nedarbosies." DVI decision of 20 March 2025: "lai apstrādātu analītiskās vai reklāmu sīkdatnes ir jāiegūst piekrišana no lietotāja". DVI (4 November 2024) lists statistics cookies among non-mandatory ones that need a choice.

**Sources.**
- https://likumi.lv/ta/id/96619-informacijas-sabiedribas-pakalpojumu-likums
- https://likumi.lv/ta/id/96619-informacijas-sabiedribas-pakalpojumu-likums/redakcijas-datums/2026/12/01
- https://www.dvi.gov.lv/lv/jaunums/dviskaidro-musu-timekla-vietne-tiek-izmantotas-sikdatnes-ludzu-piekritiet
- https://www.dvi.gov.lv/lv/media/4292/download
- https://www.dvi.gov.lv/lv/jaunums/dviskaidro-kadas-kludas-visbiezak-tiek-pielautas-sikdatnu-baneros

Date checked: 2026-11-13. Confidence: high. Status: confirmed.

---

<a id="S29"></a>
## S29. Liechtenstein: KomG Art. 61(4) and Datenschutzstelle guidance

**Supported claim.**
- Kommunikationsgesetz (LGBl. 2023 Nr. 216, LR 784.10) Art. 61(4), in force 1 February 2025: storage or access "sind nur zulässig, wenn der Nutzer auf der Grundlage von klaren und umfassenden Informationen eingewilligt hat". Consent is not needed for transmission or where storage or access "unbedingt erforderlich ist, damit der Diensteanbieter einen vom Nutzer ausdrücklich gewünschten audiovisuellen Mediendienst zur Verfügung stellen kann". (The official text says "audiovisuellen Mediendienst"; the Datenschutzstelle paraphrases it as a requested "Dienst".)
- Datenschutzstelle: the DSS "schliesst sich der grosszügigeren Auslegung der Behörden etwa aus Frankreich und Italien an" and allows, under strict conditions, cookies "und vergleichbaren Technologien für den alleinigen Zweck der Erstellung von ... einfachen Webanalysen und Besucherstatistiken auch ohne die Einholung einer ausdrücklichen Einwilligung". Its cookie page: such use falls under the Art. 61(4)(b) exemption "und es ist entsprechend nur eine Information, aber keine Einwilligung (Cookie-Banner) der Betroffenen erforderlich" where the conditions are met. Conditions: purpose solely the controller's visitor measurement, no "eigene Zwecke des Auftragsverarbeiters"; data only for anonymised statistics of one website; no matching, linking or passing on to third parties in non-anonymised form; no tracking across sites; information to users; limited storage periods; a GDPR Art. 21 objection right. A processor must keep each customer's data separate and "keine eindeutig zuordenbaren Identifikatoren pro Betroffenen verwenden".

**Note.** The last condition needs checking against the daily visitor hash, which identifies one visitor on one site for one day.

**Sources.**
- https://www.gesetze.li/konso/2023216
- https://www.datenschutzstelle.li/aktuelles/aktualisierung-cookies-und-vergleichbare-technologien
- https://www.datenschutzstelle.li/datenschutz/themen-z/cookies

Date checked: 2026-11-13. Confidence: high (regulator position), medium (statute wording). Status: confirmed.

---

<a id="S30"></a>
## S30. Lithuania: Electronic Communications Law Art. 73(4) and VDAI summary

**What the leads say (not primary).**
- Elektroninių ryšių įstatymas Nr. IX-2135, Art. 73(4) (formerly 61(3)), as shown on an unofficial mirror: storage or access "leidžiama tik su sąlyga, jeigu ... davė tam sutikimą", with exemptions for transmission and "būtinais atvejais" for an information society service ordered by the user.
- VDAI "Slapukų naudojimo praktikos apibendrinimas" (2025), as read on a third-party copy: "prieš svetainėje įgalinant bet kokios rūšies (išskyrus būtinuosius) slapukų veikimą būtina gauti svetainės lankytojo sutikimą". It classes statistics cookies as non-necessary.

**Unconfirmed.** Every official route (e-tar.lt, e-seimas.lrs.lt, vdai.lrv.lt) returned HTTP 403 or a bot challenge on 2026-11-13. The RRT page on cookies did not render. The wording above therefore comes from non-official copies and is not relied on.

**Sources tried.**
- https://www.e-tar.lt/portal/lt/legalAct/TAR.82D8168D3049/asr (failed: 403)
- https://e-seimas.lrs.lt/portal/legalAct/lt/TAD/TAIS.232036/asr (failed: 403)
- https://vdai.lrv.lt/lt/naujienos/slapuku-naudojimo-interneto-svetainese-apibendrinimas/ (failed: 403)
- https://rrt.lt/straipsniai/kas-yra-slapukai (did not render)

Date checked: 2026-11-13. Confidence: low. Status: unconfirmed (official sources unreachable).

---

<a id="S31"></a>
## S31. Luxembourg: law of 30 May 2005 Art. 4(3)(e) and CNPD guidelines

**Supported claim.**
- Loi modifiée du 30 mai 2005, Art. 4(3)(e) (consolidated version applicable at 25 December 2020; no later amending act found): storage or access only "à condition que l'abonné ou l'utilisateur ait donné son accord, après avoir reçu une information claire et complète". Exemption for transmission or storage or access "strictement nécessaires au fournisseur pour la fourniture d'un service de la société de l'information expressément demandé". Art. 4(4) makes breach a criminal offence.
- CNPD "Lignes directrices en matière de cookies et autres traceurs", version of 3 January 2022. Statistics: consent needed, "sauf exceptions": although first-party analytics "ne posent pas de risques importants", "il est néanmoins nécessaire que l'exploitant du site obtienne le consentement". Exception: "dans les cas où l'exploitant du site serait en mesure de démontrer que l'utilisation de certains cookies analytiques sont nécessaires à la fourniture du service (par exemple, parce qu'ils sont nécessaires à l'évaluation des capacités serveurs nécessaires ou à la détection de problèmes de navigation), ceux-ci pourraient être exemptés de consentement", if the data "ne sont pas transmises à des tiers ni recoupées avec d'autres traitements, ne permettent pas un suivi global de la navigation ..., sont collectées pour le compte exclusif de l'éditeur du site et servent à produire des statistiques anonymes uniquement". The same regime applies to other trackers.

**Sources.**
- https://data.legilux.public.lu/filestore/eli/etat/leg/loi/2005/05/30/n4/consolide/20201225/fr/html/eli-etat-leg-loi-2005-05-30-n4-consolide-20201225-fr-html.html
- https://cnpd.public.lu/content/dam/cnpd/fr/dossiers-thematiques/cookies/CNPD-LD-Cookies.pdf

Date checked: 2026-11-13. Confidence: medium. Status: confirmed (statute current to the 2020 consolidation).

---

<a id="S32"></a>
## S32. Malta: S.L. 586.01 reg. 5 and IDPC guidance

**Supported claim.**
- Processing of Personal Data (Electronic Communications Sector) Regulations, S.L. 586.01, reg. 5(1): storage or access "shall only be allowed on condition that the subscriber or user concerned has given his consent". Reg. 5(2) exempts transmission and storage or access "as may be strictly necessary in order for the service provider to provide an information society service explicitly requested".
- IDPC "Guidance Note on Cookies Consent Requirements" (undated on the page): "the installation of tracking cookies on users' devices is lawful insofar as the prior informed consent of the user is obtained". It refers to WP29 Opinion 04/2012 (WP 194) for exempt categories and says nothing specific about analytics.

**Sources.**
- https://legislation.mt/eli/sl/586.1/eng
- https://legislation.mt/getpdf/602258b9bc8272018c0e1dc7
- https://idpc.org.mt/idpc-publications/guidance-note-on-cookies-consent-requirements/

Date checked: 2026-11-13. Confidence: medium. Status: partly confirmed (statute confirmed; no IDPC statement on analytics).

---

<a id="S33"></a>
## S33. Norway: ekomloven § 3-15 and Nkom guidance

**Supported claim.**
- Lov om elektronisk kommunikasjon (LOV-2024-12-13-76, last amended by LOV-2025-12-22-128 from 1 July 2026) § 3-15: storage or access is not allowed unless the user is informed "og uten at den aktuelle sluttbrukeren eller brukeren har gitt samtykke. Samtykke skal oppfylle kravene til samtykke i personvernforordningen." Exemptions: "a. utelukkende for det formål å overføre kommunikasjon i et elektronisk kommunikasjonsnett, eller b. som er strengt nødvendig for å levere en informasjonssamfunnstjeneste etter den aktuelle sluttbrukerens eller brukerens uttrykkelige forespørsel." The Ministry may set exemptions by regulation; the ekomforskrift (FOR-2024-12-20-3410) contains none for cookies.
- Nkom: "Unntakene fra kravet til samtykke er snevre." "Bestemmelsen gjelder altså uavhengig av om det er snakk om personopplysninger eller ikke." Datatilsynet: Nkom decides whether the exemptions in § 3-15 are met. Neither regulator addresses analytics specifically.

**Sources.**
- https://lovdata.no/dokument/NL/lov/2024-12-13-76
- https://lovdata.no/dokument/SF/forskrift/2024-12-20-3410
- https://nkom.no/internett/informasjonskapsler-cookies
- https://www.datatilsynet.no/personvern-pa-ulike-omrader/internett-og-apper/bruk-av-informasjonskapsler-og-andre-sporingsteknologier/

Date checked: 2026-11-13. Confidence: high. Status: confirmed.

---

<a id="S34"></a>
## S34. Poland: Electronic Communications Law of 12 July 2024 Art. 399 and UODO

**Supported claim.**
- Prawo komunikacji elektronicznej (Dz.U. 2024 poz. 1221) Art. 399 ust. 1: storage or access "jest dozwolone, pod warunkiem że: 1) abonent lub użytkownik końcowy zostanie uprzednio poinformowany [...]; 2) [...] wyrazi na to zgodę". Ust. 3: the conditions do not apply where storage or access "jest konieczne do: 1) wykonania transmisji komunikatu elektronicznego [...] lub 2) dostarczania usługi telekomunikacyjnej lub usługi świadczonej drogą elektroniczną, żądanej przez abonenta lub użytkownika końcowego." The Sejm ELI API lists the act as in force with five amending acts (2025/637, 2025/820, 2026/252, 2026/815, 2026/1296); none amends Art. 399.
- UODO Biuletyn, 31 March 2025: "użytkownik musi wyrazić świadomą i jednoznaczną zgodę na stosowanie plików cookies, w szczególności tych wykorzystywanych w celach marketingowych i analitycznych."

**Not found.** UKE guidance on cookies. isap.sejm.gov.pl blocked automated access; the official journal PDF and the Sejm ELI API were used.

**Sources.**
- https://dziennikustaw.gov.pl/D2024000122101.pdf
- https://api.sejm.gov.pl/eli/acts/DU/2024/1221
- https://nowybiuletyn.uodo.gov.pl/problematyka-ochrony-danych-osobowych-a-pliki-cookies/

Date checked: 2026-11-13. Confidence: high. Status: confirmed.

---

<a id="S35"></a>
## S35. Portugal: Lei 41/2004 Art. 5 and CNPD note

**Supported claim.**
- Lei 41/2004 Art. 5 (as republished by Lei 46/2012, DR 1.ª série n.º 167, 29 August 2012). (1): storage or access "apenas são permitidos se estes tiverem dado o seu consentimento prévio, com base em informações claras e completas". (2) exempts storage or access "a) Que tenha como única finalidade transmitir uma comunicação através de uma rede de comunicações eletrónicas; b) Estritamente necessário ao fornecedor para fornecer um serviço da sociedade de informação solicitado expressamente pelo assinante ou utilizador."
- CNPD "Nota informativa" on cookies (25 June 2021): website owners must obtain consent "quando tal se impõe, como é o caso de cookies para fins de analítica, nos termos do artigo 5.º da Lei 41/2004".

**Not confirmed.** The consolidated text on diariodarepublica.pt did not render, so amendments after 2012 are not ruled out.

**Sources.**
- https://dre.pt/application/conteudo/174793
- https://www.cnpd.pt/media/x2zdus50/nota-informativa-cnpd_cookies_20210625.pdf

Date checked: 2026-11-13. Confidence: high. Status: partly confirmed (statute current to 2012 republication).

---

<a id="S36"></a>
## S36. Romania: Law 506/2004 Art. 4(5) and (6), and ANSPDCP enforcement

**Supported claim.**
- Legea 506/2004 Art. 4(5) (as amended by OUG 13/2012): storage or access "este permisă numai cu îndeplinirea în mod cumulativ a următoarelor condiţii: a) abonatul sau utilizatorul în cauză şi-a exprimat acordul; b) [...] i s-au furnizat, anterior exprimării acordului, [...] informaţii clare şi complete". Art. 4(6) exempts operations "realizate exclusiv în scopul efectuării transmisiei unei comunicări" and those "strict necesare în vederea furnizării unui serviciu al societăţii informaţionale, solicitat în mod expres de către abonat sau utilizator".
- ANSPDCP press release of 14 October 2025 (CORAL TRAVEL): the operator stored "module cookies care nu erau necesare din punct de vedere tehnic [...] fără obținerea acordului utilizatorului"; it was ordered to configure the site so that "cookie-urile nenecesare să nu fie plasate înainte de obținerea consimțământului valabil".

**Not confirmed.** legislatie.just.ro failed (protocol and TLS errors). The text was read in the consolidated copy hosted by ANSPDCP (amendments up to Legea 235/2015). The Chamber of Deputies register (not an official text) lists no later change to Art. 4(5) and (6). No ANSPDCP guidance specific to analytics was found.

**Sources.**
- https://www.dataprotection.ro/servlet/ViewDocument?id=859
- https://www.cdep.ro/ords/pls/legis/legis_pck.htp_act?ida=53465
- https://www.dataprotection.ro/?page=Comunicat_Presa_14.10.2025

Date checked: 2026-11-13. Confidence: medium-high. Status: partly confirmed.

---

<a id="S37"></a>
## S37. Slovakia: Act 452/2021 § 109(8) and ÚRSO/ÚOOÚ opinion

**Supported claim.**
- Zákon 452/2021 Z. z. § 109 ods. 8 (version in force 18 August to 31 December 2026): anyone storing or accessing information in terminal equipment "je na to oprávnený iba ak užívateľ udelil preukázateľný súhlas". Exemption for transmission, "alebo ak je to bezpodmienečne potrebné pre poskytovateľa služieb informačnej spoločnosti na poskytovanie služby informačnej spoločnosti, ktorú výslovne požaduje užívateľ."
- ÚRSO opinion on § 109(8), issued with the Slovak DPA (12 November 2025): the rules cover "akékoľvek iné nástroje založené na ukladaní a potrebe prístupu do koncového zariadenia užívateľa (napr. fingerprinting, web beacon)"; "pri ukladaní a získavaní informácií prostredníctvom všetkých druhov cookies okrem nevyhnutných cookies, je potrebný predchádzajúci súhlas dotknutej osoby." Statistical and analytics purposes need separate consent.

**Sources.**
- https://www.slov-lex.sk/ezbierky/pravne-predpisy/SK/ZZ/2021/452/
- https://www.teleoff.gov.sk/files/urad/odbory-oddelenia/odbor-statneho-dohladu-elektronickych-komunikacii/problematika-cookies/stanovisko-uradu-k-ziskavaniu-suhlasu/cookies-stanovisko_12-11-2025.pdf

Date checked: 2026-11-13. Confidence: high. Status: confirmed.

---

<a id="S38"></a>
## S38. Slovenia: ZEKom-2 Art. 225 and Information Commissioner

**Supported claim.**
- ZEKom-2 (Uradni list RS 130/22), Art. 225(1): storage or access "je dovoljeno samo pod pogojem, da je naročnik ali uporabnik v to privolil potem, ko je bil predhodno jasno in izčrpno obveščen". (2) exempts transmission and storage or access "nujno potrebno za zagotovitev storitve informacijske družbe, ki jo naročnik ali uporabnik izrecno zahtevata". (5): the Informacijski pooblaščenec supervises the article. The PISRS consolidation (to 40/25) has the same wording.
- Information Commissioner opinion 07121-1/2022/1301 (28 November 2022): "Za spletno analitiko niso določene izjeme, zato velja zahteva po privolitvi in se ni primerno zanašati na legitimen interes." Cookie FAQ: fingerprinting used "za namen izdelave analitike" needs "vnaprejšnja informirana privolitev ... enako kot pri uporabi piškotkov", unless one of the Art. 225 exemptions applies.

**Caveat.** ip-rs.si returned HTTP 403; both IP-RS pages were read through Internet Archive captures of the official URLs (2 July 2025 and 17 October 2026). The 2022 opinion carries a banner that it may no longer be current, but it applies ZEKom-2 Art. 225.

**Sources.**
- https://www.uradni-list.si/glasilo-uradni-list-rs/vsebina/2022-01-3081
- https://pisrs.si/pregledPredpisa?id=ZAKO8611
- https://www.ip-rs.si/mnenja-gdpr/varovanje-osebnih-podatkov-in-spletni-pi%C5%A1kotki-1669965271 (via archive capture)
- https://www.ip-rs.si/varstvo-osebnih-podatkov/informacijske-tehnologije-in-osebni-podatki/pi%C5%A1kotki-odgovori-na-pogosta-vpra%C5%A1anja (via archive capture)

Date checked: 2026-11-13. Confidence: high. Status: confirmed (regulator pages via archive copies).

---

<a id="S39"></a>
## S39. Spain: LSSI Art. 22.2 and AEPD audience measurement guide

**Supported claim.**
- Ley 34/2002 (LSSI) Art. 22.2 (BOE consolidated text, last update 23 January 2025): storage and retrieval devices may be used "a condición de que los mismos hayan dado su consentimiento después de que se les haya facilitado información clara y completa". Exemption: "Lo anterior no impedirá el posible almacenamiento o acceso de índole técnica al solo fin de efectuar la transmisión de una comunicación ... o, en la medida que resulte estrictamente necesario, para la prestación de un servicio de la sociedad de la información expresamente solicitado por el destinatario."
- AEPD "Uso de cookies para herramientas de medición de audiencia" (January 2024): audience measurement cookies "o tecnologías similares" can be exempt under Art. 22.2 if they "deben tener una finalidad estrictamente limitada a la medición exclusiva de la audiencia del sitio o de la aplicación. Este tratamiento ha de ser realizado en nombre exclusivo del editor y ser utilizadas para producir datos estadísticos anónimos únicamente." They must not lead to data being "cotejen con otras operaciones de tratamiento" or "se transmitan a terceros", and "se excluye cualquier solución que utilice el mismo identificador en varios sitios". Allowed measurements include page-by-page audience, referrer "por página y agregada diariamente", "tipo de dispositivo, navegador y tamaño de pantalla ... agregados diariamente" and geographic area aggregated daily; "cualquier otro tratamiento de los datos más allá de estos listados ... ha de contar con el consentimiento". Safeguards: inform users; tracker lifetime of about 13 months that "no se extenderá automáticamente en nuevas visitas"; keep data at most 25 months; review periodically. A multi-publisher provider must ensure "Los datos se recopilan, procesan y almacenan de forma independiente para cada editor", under an Art. 28 GDPR contract with no reuse.
- AEPD "Guía sobre el uso de las cookies" (May 2024), Annex II, refers to the audience measurement guide.

**Note.** "Language" is not in the AEPD list of allowed measurements. The AEPD requires anonymous statistics only; the service's exact counts and its session lists raise the same point as for France (S2, `privacy-strategy.md` section 9).

**Sources.**
- https://www.boe.es/buscar/act.php?id=BOE-A-2002-13758
- https://www.aepd.es/guias/guia-cookies-analiticas-externas.pdf
- https://www.aepd.es/guias/guia-cookies.pdf

Date checked: 2026-11-13. Confidence: high. Status: confirmed.

---

<a id="S40"></a>
## S40. Sweden: Electronic Communications Act (2022:482) ch. 9 § 28 and PTS guidance

**Supported claim.**
- Lag (2022:482) om elektronisk kommunikation, 9 kap. 28 § (amended up to SFS 2026:1681): "Uppgifter får lagras i eller hämtas från en abonnents eller användares terminalutrustning endast om abonnenten eller användaren får tillgång till information om ändamålet med behandlingen och samtycker till den." Exemptions: storage or access that "1. behövs för överföring av ett elektroniskt meddelande via ett elektroniskt kommunikationsnät, eller 2. är nödvändig för tillhandahållande av en tjänst på uttrycklig begäran av användaren eller abonnenten."
- PTS (page updated 14 May 2024): "För att undantaget ska gälla krävs det att kakan är nödvändig för att en tjänst som användaren begär ska fungera. Det handlar inte om vilka kakor du tycker är nödvändiga, till exempel samla in statistik."

**Not found.** IMY guidance on Art. 5(3) exemptions for analytics.

**Sources.**
- https://www.riksdagen.se/sv/dokument-och-lagar/dokument/svensk-forfattningssamling/lag-2022482-om-elektronisk-kommunikation_sfs-2022-482/
- https://pts.se/internet-och-telefoni/kakor-cookies/

Date checked: 2026-11-13. Confidence: high. Status: confirmed.

---

## Summary

| S-id | Claim | Status | Confidence |
|---|---|---|---|
| S1 | EDPB Guidelines 2/2023 v2.0 scope, JavaScript instruction is access | Confirmed | High |
| S2 | CNIL audience measurement exemption, tool, non-cookie scope | Confirmed | High (conditions) |
| S3 | UK DUAA 2025 s.112 / Sch. 12, in force 5 Feb 2026, ICO guidance | Confirmed | High |
| S4 | Germany § 25 TDDDG, DSK on reach measurement, reform | Confirmed (law, DSK); Omnibus confirmed as a proposal not adopted (S14) | High |
| S5 | Netherlands Tw Art. 11.7a low-impact analytics | Partly confirmed (AP guidance on non-cookie not found) | High (statute), medium (AP) |
| S6 | Italy Garante 10 June 2021 analytics conditions | Confirmed, wording to recheck | Medium |
| S7 | Turkey cookie guide (July 2025: first-party analytics under Kriter B), Law 7499, 2024 Regulation, no EU adequacy | Confirmed (2022 edition of the guide not obtained) | High |
| S8 | CJEU C-413/23 P EDPS v SRB | Confirmed (application is inference) | High |
| S9 | Latombe T-553/23 and appeal C-703/25 P | Confirmed (pending; order of 4 June 2026 not read) | High |
| S10 | GDPR Art. 11 and Art. 28 | Confirmed | High |
| S12 | California CCPA/CPRA: opt-out regime, analytics by a service provider is a business purpose | Confirmed (statute); partly confirmed (regulation wording) | High (statute), medium (regulations) |
| S13 | Brazil LGPD Arts. 7, 10, 12 and ANPD cookie guide: legitimate interest for aggregate audience measurement | Confirmed (Planalto unreachable, Câmara and Senado used) | High (text), medium-high (application) |
| S14 | EU Digital Omnibus COM(2025) 837: not adopted, Council mandate 21 Oct 2026, EP committee vote pending | Confirmed | High (stage), medium-high (mandate content) |
| S15 | Austria TKG 2021 § 165(3), DSB: analytics needs consent | Confirmed (telecom authority guidance not checked) | Medium-high |
| S16 | Belgium law of 30 July 2018 Art. 10/2, APD: analytics needs consent | Confirmed | High |
| S17 | Bulgaria ZET Art. 4a (opt-out wording), CPDP: consent unless anonymised and not shared | Partly confirmed (statute and guidance inconsistent) | Medium |
| S18 | Croatia ZEK Art. 43(4), AZOP: analytics needs consent | Confirmed | High |
| S19 | Cyprus Law 112(I)/2004 Art. 99(5), Commissioner: analytics needs consent | Confirmed | High |
| S20 | Czechia Act 127/2005 § 89(3), ÚOOÚ: analytics needs consent | Confirmed | High |
| S21 | Denmark BEK 1148/2011, DIGST and Datatilsynet: statistics need consent | Confirmed | High |
| S22 | Estonia ESS § 102, AKI: identifying analytics need consent | Partly confirmed (statutory mapping unclear) | Medium |
| S23 | Finland Act 917/2014 § 205, Traficom: analytics only exceptionally necessary | Confirmed | Medium |
| S24 | Greece Law 3471/2006 Art. 4(5), DPA 1/2020: consent in principle | Partly confirmed (statute current to 2019) | Medium |
| S25 | Hungary Eht. § 155(4), NAIH 2017: consent, no analytics exemption | Partly confirmed | Medium |
| S26 | Iceland Act 70/2022 Art. 88(2), Fjarskiptastofa 14/2024: narrow reading | Partly confirmed (current consolidation not read) | Medium |
| S27 | Ireland S.I. 336/2011 reg. 5, DPC: analytics needs consent | Confirmed (amendments unverified) | High |
| S28 | Latvia ISPL Art. 7.1, DVI: analytics needs consent | Confirmed | High |
| S29 | Liechtenstein KomG Art. 61(4), Datenschutzstelle: simple analytics without consent under conditions | Confirmed | High (regulator), medium (statute) |
| S30 | Lithuania ERI Art. 73(4), VDAI | Unconfirmed (official sources returned 403) | Low |
| S31 | Luxembourg law of 30 May 2005 Art. 4(3)(e), CNPD: consent unless necessity shown | Confirmed | Medium |
| S32 | Malta S.L. 586.01 reg. 5, IDPC | Partly confirmed (IDPC silent on analytics) | Medium |
| S33 | Norway ekomloven § 3-15, Nkom: narrow exemptions | Confirmed | High |
| S34 | Poland PKE Art. 399, UODO: analytics needs consent | Confirmed | High |
| S35 | Portugal Lei 41/2004 Art. 5, CNPD: analytics needs consent | Partly confirmed (statute current to 2012) | High |
| S36 | Romania Law 506/2004 Art. 4(5)-(6), ANSPDCP enforcement | Partly confirmed (DPA-hosted text) | Medium-high |
| S37 | Slovakia Act 452/2021 § 109(8), ÚRSO and DPA: analytics needs consent | Confirmed | High |
| S38 | Slovenia ZEKom-2 Art. 225, IP-RS: analytics needs consent | Confirmed (IP-RS via archive copies) | High |
| S39 | Spain LSSI Art. 22.2, AEPD audience measurement guide: exempt under conditions | Confirmed | High |
| S40 | Sweden LEK ch. 9 § 28, PTS: statistics are not necessary | Confirmed | High |
