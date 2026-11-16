# caddy 2.11.9 with the one plugin the platform needs:
#   github.com/caddy-dns/cloudflare          dns-01 challenges in the cloudflare
#       zone of BASE_DOMAIN on deployed nodes. v0.2.4 (2026-03-23) is the newest
#       release; it built against 2.11.9 on 2026-11-13 and resolves
#       libdns/cloudflare v0.2.2 and libdns v1.1.1
# replace-response is gone with umami's public site: no served page is
# rewritten any more, so the binary carries no response rewriting handler
#
#   docker build -f deploy/images/caddy.Dockerfile -t veristar/caddy:2.11.9 deploy/images
#
# the build context is unused; any directory works. base images are pinned by
# tag and index digest (resolved 2026-11-12).

FROM caddy:2.11.9-builder@sha256:345264d8c6b50f44f4a4d507d4e6b6bcae40eef2f210d70f76e717fd8cc31baf AS build

# the builder image carries xcaddy; the plan pins 0.4.7, so a different one
# stops the build instead of producing an unreviewed binary
RUN test "$(xcaddy version | cut -d' ' -f1)" = "v0.4.7" || { echo "xcaddy is not v0.4.7: $(xcaddy version)" >&2; exit 1; }

RUN xcaddy build v2.11.9 --output /out/caddy \
      --with github.com/caddy-dns/cloudflare@v0.2.4 \
      # caddy 2.11.9 requires golang.org/x/net v0.60.0, which has fixable high
      # findings (CVE-2026-78661 and others, trivy 2026-11-13); v0.62.0 is the
      # pinned version. remove this line once a caddy release requires it.
      --replace golang.org/x/net=golang.org/x/net@v0.62.0

FROM caddy:2.11.9@sha256:e40b94d0a43b5ff69cb20568d73f10c70703df7b0ffc2298eb3d723cdc9d0bd1

COPY --from=build /out/caddy /usr/bin/caddy

# prove the binary is 2.11.9, carries the dns module and no response
# rewriting handler, so a silent build change cannot ship another set
RUN caddy version | grep -q '^v2\.11\.9 ' \
 && caddy list-modules | grep -qx 'dns.providers.cloudflare' \
 && ! caddy list-modules | grep -qx 'http.handlers.replace_response'

# the official image runs caddy as root. caddy needs no root here: docker sets
# net.ipv4.ip_unprivileged_port_start to 0 inside a container's network
# namespace, so an unprivileged process binds 80 and 443 without any
# capability, and the compose files drop every capability. file capabilities
# would not help anyway, because no-new-privileges ignores them. /data and
# /config (certificates, the local ca, autosave) belong to this user, so a
# fresh named volume starts with the right owner. a volume created by an
# older root image keeps root ownership and must be chowned to 10001 once
# (docs/runbooks/security-patch.md, "caddy runs as non-root").
# the copied binary keeps the base image's cap_net_bind_service file
# capability. with every capability dropped and no-new-privileges, the
# kernel refuses to exec a file whose capabilities it cannot grant
# ("operation not permitted"), and no capability is needed (see above), so
# the file capability is removed.
RUN setcap -r /usr/bin/caddy \
 && addgroup -S -g 10001 caddy \
 && adduser -S -D -H -u 10001 -G caddy -h /data caddy \
 && chown -R 10001:10001 /data /config
USER 10001:10001
