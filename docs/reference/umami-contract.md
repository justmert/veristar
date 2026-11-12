# Umami contract

What the platform relies on in Umami, confirmed by running the pinned image. The checks
are automated and form the upgrade gate (`make umami-compat`, plan phases 5 and 18).

- Image: `ghcr.io/umami-software/umami:3.5.0@sha256:b64f40400d77bcadd39cf5c62a1e368c99524ca442024f96e1133355a19a5dbc`
- Source read alongside: tag `v3.5.0`, commit `d3c62a805beda8643cb468924dda38617fb4c286`. File paths
  below are relative to that tree.
- Environment under test: `deploy/compose/dev.yml`, including
  `CLIENT_IP_HEADER=x-edge-client-ip`, `ENABLE_CLIENT_SALT=1`, `SALT_ROTATION=daily`,
  `DISABLE_LOGIN=1`.
- Checks: `tests/umami-compat/*_test.go` (Go, build tag `umamicompat`) and
  `tests/umami-compat/specs/ui.spec.ts` (Playwright, Chromium). Verified 2026-11-12.

## Results

| # | Question | Answer | Check | Source |
|---|---|---|---|---|
| 1 | Does `timestamp` in seconds set the stored time? | Yes. Event `createdAt`, session `firstAt` and `lastAt` equal the timestamp, and reports bucket by it. | `TestTimestampSecondsStored` | `src/app/api/send/route.ts` (`new Date(timestamp * 1000)`) |
| 2 | Does `salt` split sessions with `ENABLE_CLIENT_SALT=1`? | Yes. Different salts give two sessions; the same salt, user agent and website give one. | `TestClientSaltSplitsSessions` | `src/lib/collection-session.ts` (`hash(baseSalt, salt)`) |
| 3 | `x-edge-client-ip: 192.0.2.1` with `cf-ipcountry: DE`? | Session country `DE`, region and city null. | `TestCountryHeaderWithTestNetAddress` | `src/lib/detect.ts` `getLocation` |
| 4 | `192.0.2.1` with no country header? | **200**, country, region and city null. The stop condition of the design is not hit. The bundled `GeoLite2-City.mmdb` has no entry for TEST-NET-1. | `TestNoCountryHeaderStoresNullCountry` | `src/lib/detect.ts`, `/app/geo/GeoLite2-City.mmdb` in the image |
| 5 | `ip` in the body? | Country headers are then ignored (country null even with `cf-ipcountry`). The replayer must send the address only as a header. | `TestPayloadIPIgnoresCountryHeader` | `getClientInfo` passes `skipHeaders = !!payload.ip` |
| 6 | Bot user agent? | 200, body exactly `{"beep":"boop"}`, no session, no pageview (Googlebot, bingbot). | `TestBotUserAgentStoresNothing` | `isbot` check in `route.ts` |
| 7 | Visit id and `x-umami-cache`? | With the cache token the visit and session are kept, also across UTC hours. Without it the visit id is derived from session and UTC hour: two events 45 minutes apart in one hour share a visit, an event in the next hour starts a new one. A cache token of another website is ignored. | `TestVisitIDCacheAndHourRule`, `TestCacheTokenBoundToWebsite` | `resolveCollectionSession`: `visitSalt = hash(startOfHour(createdAt))`, expiry skipped when `historical` |
| 8 | `DISABLE_LOGIN=1`? | `GET /login` is 403; `POST /api/auth/login` still works. | `TestDisableLoginBlocksOnlyThePage` | `docker/proxy.ts` `disableLogin` |
| 9 | UI path of a team website | `/websites/<id>` renders the full overview for a team member. Umami's own navigation inside a team uses `/teams/<teamId>/websites/<id>`, which `next.config.ts` rewrites to `/websites/<id>`; both show all seven metrics. The personal list `/websites` is empty for this role; team websites are listed at `/teams/<teamId>/websites`. The SSO target `/websites/<id>` of the design works. | `ui.spec.ts` | `next.config.ts` rewrite `/teams/:teamId/:path*`, `src/components/hooks/useNavigation.ts` `renderUrl` |
| 10 | What can a `view-only` user with `team-view-only` open? | Overview, events, sessions, realtime, compare, breakdown and the other report pages of the team website; team settings (read-only member list, "Leave" button); the website settings page and `/admin/users` render their frame, but every write is refused by the API (401). The UI still shows Save, Add (share), Transfer, Reset and Delete buttons; pressing Save shows "Unauthorized" and changes nothing. | `ui.spec.ts`, `TestViewOnlyTeamMemberReadsButCannotChange` | `src/lib/constants.ts` `ROLE_PERMISSIONS` (`view-only` and `team-view-only` are empty) |
| 11 | Are all seven RFP metrics visible to that role? | Yes, on the overview: Visitors, Visits, Views, Bounce rate, Visit duration in the metrics bar; Referrers (Sources panel), Countries (Location panel), Browsers, OS and Devices (Environment panel). Via the API the role reads `/stats`, `/metrics` and `/pageviews` of its team's websites and gets 401 for other teams. | `ui.spec.ts`, `TestViewOnlyTeamMemberReadsButCannotChange`, `TestViewOnlyCannotReadOtherTeam` | `src/app/(main)/websites/[websiteId]/WebsiteMetricsBar.tsx`, `WebsitePanels.tsx` |
| 12 | Does `/sso` replace the history entry? | **No.** `SSOPage` calls `router.push(url)`. After SSO the tab history holds `/sso?token=<token>&url=...` followed by the target. Pressing Back lands on `/sso`, which stores the token again and pushes the target. | `ui.spec.ts` (Navigation API entries) | `src/app/sso/SSOPage.tsx` |
| 13 | Route list | 141 paths, 186 operations in `/openapi.json` of the image, pinned in `tests/umami-compat/testdata/openapi-routes.txt`. | `TestOpenAPIRouteListUnchanged` | `/app/public/openapi.json` in the image, generated by `scripts/generate-openapi.ts` |

## Request and response shapes used by `internal/umami`

- `POST /api/send` (no auth): body `{type:"event", payload:{website, hostname, url, referrer, title,
  screen, language, name, data, timestamp, salt}}`. 200 `{cache, sessionId, visitId}`; bot 200
  `{"beep":"boop"}`; unknown website 400. `salt` max 100 characters. A desktop user agent with a
  screen up to 1920 wide is stored as device `laptop` (`getDevice`).
- `POST /api/auth/login`: `{token, user}`. Every other route needs `Authorization: Bearer`.
- `POST /api/users` `{id?, username, password(8..255), role}` with role required
  (`admin|user|view-only`); answer `{id, username, role}`. Duplicate username 400.
- `POST /api/users/<id>` `{username?, password?, role?}`; an admin sets a password without the
  current one, and the user's old tokens are refused afterwards. `DELETE /api/users/<id>` `{ok:true}`.
- `POST /api/teams` `{name(<=50), ownerId?}`: answer is the array `[team, teamUser]`, the second
  being the operator membership with role `team-owner`. The team id is chosen by Umami.
- `POST /api/teams/<id>/users` `{userId, role}` with role `team-manager|team-member|team-view-only`;
  duplicate 400. `DELETE /api/teams/<id>/users/<userId>` `{ok:true}`.
- `DELETE /api/teams/<id>` deletes the team with its websites and their data.
- `POST /api/websites` `{id, name, domain, teamId}`: the caller's id is kept; ids share one
  namespace with all entities, a used id is 400. `DELETE /api/websites/<id>` deletes the row and
  data (no `CLOUD_MODE`). `POST /api/websites/<id>/reset` deletes all events and sessions.
- `DELETE /api/websites/<id>/sessions/<sessionId>`: `{ok:true}`, unknown session 404.
- Read routes take `startAt`/`endAt` in unix milliseconds. `/stats` answers
  `{pageviews, visitors, visits, bounces, totaltime, comparison:{...}}` (totaltime in seconds).
  `/metrics?type=` accepts `path, fullPath, entry, exit, referrer, domain, title, query, event, tag,
  hostname, utm*, browser, os, device, screen, language, country, region, city, distinctId,
  channel`, anything else is 400; rows `[{x, y}]`. `/pageviews?unit=&timezone=` answers
  `{pageviews:[{x,y}], sessions:[{x,y}]}`.
- Errors are `{"error":{"message","code","status"}}`. **A permission denial is 401, the same as a
  bad token** (`src/lib/response.ts` `unauthorized`), so the client logs in again once on 401 and
  then returns the error. **Deleting an unknown user, team or website, or resetting an unknown
  website, answers 500 with an empty body**, so callers check existence instead of retrying deletes.

## Deny list for `web.<BASE_DOMAIN>`

The dashboard of a view-only tenant uses only these calls (recorded by `ui.spec.ts` across the
overview, sessions, events, realtime, compare, settings and team pages):
`POST /api/auth/verify` and `GET` on `/api/heartbeat`, `/api/2fa/status`, `/api/realtime/<id>`,
`/api/teams/<id>`, `/api/teams/<id>/users`, `/api/teams/<id>/websites`, `/api/users/<id>/teams`,
`/api/users/<id>/websites`, `/api/websites/charts` and the read routes under `/api/websites/<id>`.

Findings that extend the list in the design:

1. `POST /api/me/api-keys` **succeeds for a view-only user** and returns an Umami API key. It is
   already on the list; it must stay there.
2. `/api/2fa/*` except `GET /api/2fa/status`: a tenant enabling 2FA on its Umami user would break
   control-plane SSO (login then returns `requiresTwoFactor`). Setup currently fails with 503
   because `TWO_FACTOR_ENCRYPTION_KEY` is unset, which is configuration, not a guarantee.
3. Not on the list yet and not needed by tenants: `/openapi.json` (served publicly),
   `/telemetry.js` and `/api/scripts/telemetry`, `/api/auth/sso` (needs Redis; per source it answers 500 without it),
   `/api/auth/subscription` (cloud), `/api/links*`, `/api/pixels*`, `/api/boards*`,
   `/api/websites/*/recorder`, `/api/websites/*/replays*`, `/api/websites/*/heatmaps`,
   `/api/websites/*/errors*`, `/api/websites/*/performance/*` (features not shipped),
   `/api/websites/*/transfer`, `/api/websites/*/reset`, `/compat/*` and the legacy rewrites
   `/api/reports/*` and `/api/websites/*/reports` (outside the OpenAPI contract, `next.config.ts`).
4. Simplest rule that covers the above and future write routes: from outside, allow only `GET`,
   `HEAD` and `OPTIONS` on `/api/*`, plus `POST /api/auth/verify` and `POST /api/auth/logout`, then
   apply the path denials. Write routes stay reachable on the private network for the control
   plane. Stream (c) of this stage confirms the UI with this rule.

## Other observations

- The UI offers "Join team" in settings; `/api/teams/join` with an access code adds the caller to
  that team. The code is hidden from `team-view-only` members (`GET /api/teams/<id>`), and the
  route is on the deny list.
- `GET /api/websites/<id>/export` returns a zip of aggregate CSVs (events, pages, referrers,
  browsers, os, devices, countries) to any member; no raw rows.
