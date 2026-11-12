# caddy 2.11.9 with the two plugins the platform needs:
#   github.com/caddyserver/replace-response  injects the db-ip attribution
#       link into umami's html on web.<BASE_DOMAIN> (the reference docs section 5)
#   github.com/caddy-dns/bunny               dns-01 challenges (this stage)
#
#   docker build -f deploy/images/caddy.Dockerfile -t stellar-analytics/caddy:2.11.9 deploy/images
#
# the build context is unused; any directory works. base images are pinned by
# tag and index digest (resolved 2026-11-12).

FROM caddy:2.11.9-builder@sha256:345264d8c6b50f44f4a4d507d4e6b6bcae40eef2f210d70f76e717fd8cc31baf AS build

# the builder image carries xcaddy; the plan pins 0.4.7, so a different one
# stops the build instead of producing an unreviewed binary
RUN test "$(xcaddy version | cut -d' ' -f1)" = "v0.4.7" || { echo "xcaddy is not v0.4.7: $(xcaddy version)" >&2; exit 1; }

# replace-response has no tagged releases, so its pseudo-version is the pin
RUN xcaddy build v2.11.9 --output /out/caddy \
      --with github.com/caddyserver/replace-response@v0.0.0-20250618171559-80962887e4c6 \
      --with github.com/caddy-dns/bunny@v1.2.0

FROM caddy:2.11.9@sha256:e40b94d0a43b5ff69cb20568d73f10c70703df7b0ffc2298eb3d723cdc9d0bd1

COPY --from=build /out/caddy /usr/bin/caddy

# prove the binary is 2.11.9 and carries both modules, so a silent build
# change cannot ship an image without the attribution handler
RUN caddy version | grep -q '^v2\.11\.9 ' \
 && caddy list-modules | grep -qx 'http.handlers.replace_response' \
 && caddy list-modules | grep -qx 'dns.providers.bunny'
