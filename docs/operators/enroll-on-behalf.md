---
title: "Enrolling a project on its behalf"
description: "Create a project for a console user from the command line, add its site and token, and invite its team."
---

You can enroll a project for someone with `platformctl tenant create`. The person becomes the
project's owner, and the command adds the site and an OTLP token in the same step. Use it to
onboard projects in bulk, such as SCF projects before an opening, or when you work from a network
that does not reach the console.

The command checks every value with the same rules as the console's enrollment form, writes the
same rows and audits each one with the actor `system:platformctl-tenant`.

## Before you start

The person signs in to the console once with GitHub, at `https://app.<BASE_DOMAIN>`. Signing in
creates their console account, and `tenant create` makes an existing account the owner. For a
GitHub login that never signed in, the command stops and names that step.

You need:

- the person's GitHub id or login;
- the project's name, its `https` website and a short text on how it uses Stellar;
- for a web site: its domain and every origin its pages load from;
- for a cohort: the program, round, category and award date.

## Run the command

`platformctl` runs on the core node that holds the Patroni leader, with the flags of
[bootstrap step 10](../deploy/bootstrap.md). Set `INGEST_ORIGIN` to the ingest hostname, so the
script tag points at it:

```sh
ssh <core node> sudo docker run --rm --network veristar-core_default \
  --env-file /etc/veristar/env/platformctl.env \
  -v /etc/veristar/tls/ca:/etc/sa-tls:ro \
  -e PLATFORM_DB_HOST=haproxy -e PLATFORM_DB_PORT=5000 -e PLATFORM_DB_NAME=platform \
  -e PLATFORM_DB_SSLMODE=verify-full -e PLATFORM_DB_SSLROOTCERT=/etc/sa-tls/ca.crt \
  -e INGEST_ORIGIN=https://in.<BASE_DOMAIN> \
  ghcr.io/justmert/veristar/platformctl:<release> \
  tenant create -owner-github-login octo-dev -name "Anchor Pay" \
    -website https://anchorpay.org -stellar-usage "Soroban payment contracts" \
    -program "Build Award" -round "SCF #30" -category Applications -award-date 2025-03-01 \
    -site-domain anchorpay.org -origin https://anchorpay.org -origin https://app.anchorpay.org \
    -otlp-token-name production
```

Add `-dry-run` first. It runs every check and every write inside a transaction and rolls it back,
so it reports a taken slug or an unknown owner without changing anything.

### Flags

| Flag | Required | Meaning |
|---|---|---|
| `-owner-github-id N` or `-owner-github-login L` | one of them | the console user who becomes the owner |
| `-name` | yes | project name, 1 to 100 characters |
| `-website` | yes | the project's `https` website |
| `-stellar-usage` | yes | how the project uses Stellar, 1 to 2,000 characters |
| `-slug` | no | the project's slug; derived from the name when absent |
| `-description` | no | up to 1,000 characters |
| `-stellar-id` | no | a Stellar account or contract id; repeat it for up to 20 |
| `-scf-round-url` | no | an `https` link to the project's SCF round |
| `-program`, `-round`, `-category`, `-award-date` | no | the cohort, written as the owner's own proposal; the award date is `YYYY-MM-DD` |
| `-site-domain` and `-origin` | no | one site: its domain and its allowed origins; repeat `-origin` for up to 20 |
| `-site-name` | no | the site's name; the project name when absent |
| `-otlp-token-name` | no | creates one OTLP token with this name |
| `-otlp-token-environment` | no | the token's environment label |
| `-approve` | no | creates the project `active` while enrollment needs approval |
| `-dry-run` | no | checks and reports, then rolls back |
| `-tracker-releases`, `-ingest-origin` | no | override `TRACKER_RELEASE_DIR` and `INGEST_ORIGIN` |

A value the console refuses is refused here with the flag's name, for example
`invalid flags: -origin is origin outside domain`.

### Output

The command prints one `key=value` line each:

| Key | Value |
|---|---|
| `tenant_id`, `slug`, `status` | the new project; `status` is `active`, or `pending` in approval mode |
| `owner_user_id`, `owner_github_login` | the owner |
| `cohort_proposal` | `true` when the cohort flags wrote a proposal |
| `scf_proposals` | proposals the SCF award matching wrote ([scf-awards.md](scf-awards.md)) |
| `site_id`, `site_status`, `tracker_version` | the site and the tracker release of its tag |
| `script_tag` | the tag the site pastes into its pages, the same one the console's snippet page shows |
| `otlp_token_prefix`, `otlp_token` | the token; the full value prints once and is never shown again |

Send the script tag and the token to the project over a private channel. The jobs `reconcile`
task sets up the Umami websites and the gateway and collector registry entries within 10 minutes;
until then, the gateway refuses the site's events and the collector refuses the token.

## What the owner sees

At their next sign-in, the owner finds the project in the console with a notice to accept the terms
of service. The command never accepts the terms for them. Until an owner accepts them, every
member can open the project, read its dashboards and copy the snippet, and every change is refused
with `terms_not_accepted`: editing the project, adding sites, tokens or members.

## Enrollment mode

In `open` mode the project is `active` at once. In `approval` mode it is `pending` with its site in
review, as any enrollment ([review-and-enrollment.md](review-and-enrollment.md)), and the command
refuses `-otlp-token-name` because tokens need an active project. Add `-approve` to create it
`active`: the command then writes a `tenant.approve` audit row next to `tenant.create`. In `open`
mode `-approve` is refused.

A site whose domain matches a site of a removed project goes to the review queue in both modes, as
in the console.

## Invite the team

`platformctl tenant invite` adds a person to a project the way an owner does in the console. A
GitHub account that has signed in becomes a member at once; any other account gets an invite that
its first sign-in accepts. Invites last 14 days.

The command looks the login up on GitHub with the console's OAuth app, so it also reads the
control plane's environment file:

```sh
ssh <core node> sudo docker run --rm --network veristar-core_default \
  --env-file /etc/veristar/env/platformctl.env --env-file /etc/veristar/env/controlplane.env \
  -v /etc/veristar/tls/ca:/etc/sa-tls:ro \
  -e PLATFORM_DB_HOST=haproxy -e PLATFORM_DB_PORT=5000 -e PLATFORM_DB_NAME=platform \
  -e PLATFORM_DB_SSLMODE=verify-full -e PLATFORM_DB_SSLROOTCERT=/etc/sa-tls/ca.crt \
  ghcr.io/justmert/veristar/platformctl:<release> \
  tenant invite -slug anchor-pay -github-login octo-cto -role owner
```

| Flag | Meaning |
|---|---|
| `-tenant ID` or `-slug S` | the project |
| `-github-login L` | the person's GitHub login |
| `-role` | `member` (the default) or `owner` |
| `-dry-run` | checks and reports, then rolls back |

It prints `kind=member` with `user_id`, or `kind=invite` with `invite_id` and `expires_at`. The
invite names the project's first owner as its sender, and the audit row names
`system:platformctl-tenant`. A project holds at most 50 members and open invites.

## Find what the command did

```sql
select at, action, target, detail from cp.audit_log
where actor = 'system:platformctl-tenant' order by at desc limit 20;
```
