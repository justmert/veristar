---
title: "Certificate problems"
sidebarTitle: "Certificates"
description: "Diagnose and fix certificate issuance and renewal on edge and core nodes."
---

Every node's Caddy gets its own certificates from Let's Encrypt with the ACME DNS-01 challenge
in the Cloudflare zone (`caddy-dns/cloudflare`, with the environment's Cloudflare API token
`cloudflare_api_token`), for the hostnames it serves: `in.<BASE_DOMAIN>` and
`otlp.<BASE_DOMAIN>` on the edge tier, `app.<BASE_DOMAIN>` and `web.<BASE_DOMAIN>` on the core
tier, and `status.<BASE_DOMAIN>` on a probe host in the inventory. DNS-01 is used because every
node serves the same names, so an HTTP or TLS-ALPN challenge could land on another node ([../deploy/ansible.md](../deploy/ansible.md)
section 3). The external probe bundle on its own host gets `status.<BASE_DOMAIN>` with HTTP-01
instead ([../deploy/probe.md](../deploy/probe.md)). Certificates live in the node's `caddydata` volume under `/data/caddy/certificates/`.
The zone's CAA record allows only `letsencrypt.org` and forbids wildcards.

Alerts that lead here: `CertificateExpiringSoon` (a node serves a certificate that expires
within 14 days, read by the prober over a connection to the node's own address) and
`ProbeNodeCheckFailing` with a TLS error ([alerts.md](alerts.md)). The port-80 DNS health check
does not see TLS, so a node with a broken certificate stays in DNS until you drain it.

## Steps

1. **See what the node serves**, from the controller or the probe:

   ```sh
   echo | openssl s_client -connect <node public address>:443 -servername in.<BASE_DOMAIN> 2>/dev/null \
     | openssl x509 -noout -subject -issuer -dates -serial
   ```

   Expected: issuer Let's Encrypt, `notAfter` more than 14 days away. Repeat for each name the
   node serves.

2. **Take the node out of DNS** if it serves an expired or wrong certificate:
   [edge-node-failure.md](edge-node-failure.md) step 2 (the drain flag works the same on core
   nodes).

3. **Read Caddy's log** on the node:

   ```sh
   cd /opt/veristar/<edge or core>
   sudo docker compose logs --since 24h caddy | grep -i -E 'acme|certificate|obtain|renew|dns|challenge' | tail -50
   ```

   Common causes:
   - **Cloudflare token** rejected or missing: the log names an authorization error from the
     DNS provider. Check `cloudflare_api_token` in the SOPS file and that the token is active
     with `Zone:Zone:Read` and `Zone:DNS:Edit` on the zone
     (`curl -s -H "Authorization: Bearer $TOKEN" https://api.cloudflare.com/client/v4/user/tokens/verify`).
     Fix it as in [secret-rotation.md](secret-rotation.md), which rewrites
     `/etc/veristar/env/caddy.env`.
   - **CAA or zone delegation**: the domain's name servers must be Cloudflare's and the CAA must
     allow `letsencrypt.org`. `dig +short CAA <BASE_DOMAIN>` and `dig +short NS <BASE_DOMAIN>`.
   - **Let's Encrypt rate limits**: the log says so and when to retry. Do not loop restarts.
   - **Clock**: ACME fails with a skewed clock. `chronyc tracking` on the node; the base role
     checks the offset at every converge.

4. **Retry** once the cause is fixed:

   ```sh
   sudo docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile
   ```

   If Caddy does not retry by itself, force a fresh issuance by removing the stored certificates
   and restarting Caddy (the renewal drill `tests/drills/cert-renewal.sh` does the same). The node
   serves nothing on 443 until the new certificate arrives, so drain it first:

   ```sh
   sudo docker compose exec -T caddy sh -c 'rm -rf /data/caddy/certificates/*'
   sudo docker compose restart caddy
   ```

5. **Check** with step 1, then undrain ([edge-node-failure.md](edge-node-failure.md) step 5).
   `CertificateExpiringSoon` resolves at the prober's next run.

6. **Caddy cannot write its storage** (`permission denied` under `/data`): the volume was created
   by an older root image. Fix the file ownership as in [security-patch.md](security-patch.md)
   section 6.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-13 | Production converge: issuance through Let's Encrypt and Cloudflare DNS-01 on all three nodes | Let's Encrypt certificates for `in`, `otlp`, `app` and `web` on every node, valid until 2027-02-11 | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md#checks) |
| 2026-11-14 | External probe host: issuance for `status.veristar.app` through HTTP-01 | Let's Encrypt certificate for `status.veristar.app`, valid to 2027-02-12 | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "External probe and status page" |
| 2026-11-13 | Alert fire-drill: a 7-day certificate served on edge-2 of the observability test stack | CertificateExpiringSoon and the Gatus check for that node fired at 01:34:48 and resolved at 01:39:48 after the normal certificate was back | [alerts.md](alerts.md), fire-drills |
