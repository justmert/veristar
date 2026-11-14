# the core node's caddy: the custom caddy build (caddy.Dockerfile) with the
# console single-page app at /srv/console, which deploy/caddy/app.Caddyfile
# serves as CONSOLE_ROOT, and the theme of umami's dashboard at
# /srv/umami-theme, which deploy/caddy/web.Caddyfile serves (docs/adr/0004).
# shipping them in one image makes a release and the caddy that serves it one
# atomic deploy.
#   docker build -f deploy/images/console.Dockerfile \
#     --build-arg CADDY_IMAGE=veristar/caddy:2.11.9 -t veristar/console:dev .
# CADDY_IMAGE is required: it names the caddy.Dockerfile build to extend.
# base images are pinned by tag and index digest (resolved 2026-11-12).

ARG CADDY_IMAGE

FROM node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc AS build

WORKDIR /src
# node 26 no longer ships corepack, so pnpm is installed at the version
# package.json's packageManager names
RUN npm install -g pnpm@12.18.0 && pnpm --version | grep -qx 12.18.0
COPY . .
RUN pnpm install --frozen-lockfile --filter "@veristar/console..." \
 && pnpm --filter @veristar/console build \
 && test -f apps/console/dist/index.html

FROM ${CADDY_IMAGE}
COPY --from=build /src/apps/console/dist /srv/console
# committed files, checked by `pnpm check` (deploy/caddy/umami-theme/assets.ts);
# the test stops the build when the stylesheet web.Caddyfile links is missing
COPY deploy/caddy/umami-theme/site /srv/umami-theme
RUN test -f /srv/umami-theme/_veristar/theme.css
