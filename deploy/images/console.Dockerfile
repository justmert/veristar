# the core node's caddy: the custom caddy build (caddy.Dockerfile) with the
# console single-page app at /srv/console and the public website (apps/site)
# at /srv/site, which deploy/caddy/app.Caddyfile serves as CONSOLE_ROOT and
# SITE_ROOT. shipping them in one image makes a release and the caddy that
# serves it one atomic deploy.
#   docker build -f deploy/images/console.Dockerfile \
#     --build-arg CADDY_IMAGE=veristar/caddy:2.11.9 -t veristar/console:dev .
# CADDY_IMAGE is required: it names the caddy.Dockerfile build to extend.
# SITE_ORIGIN and REPO_URL are optional and reach the website build as
# VITE_SITE_ORIGIN (its canonical address; the site's own default applies
# when empty) and VITE_REPO_URL (the source repository links; hidden when
# empty), as apps/site/README.md describes.
# base images are pinned by tag and index digest (resolved 2026-11-12).

ARG CADDY_IMAGE

FROM node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc AS build
ARG SITE_ORIGIN=
ARG REPO_URL=

WORKDIR /src
# node 26 no longer ships corepack, so pnpm is installed at the version
# package.json's packageManager names
RUN npm install -g pnpm@12.18.0 && pnpm --version | grep -qx 12.18.0
COPY . .
RUN pnpm install --frozen-lockfile --filter "@veristar/console..." --filter "@veristar/site..." \
 && pnpm --filter @veristar/console build \
 && test -f apps/console/dist/index.html
# an empty value is left unset, so the website build applies its own default
RUN if [ -n "$SITE_ORIGIN" ]; then export VITE_SITE_ORIGIN="$SITE_ORIGIN"; fi \
 && if [ -n "$REPO_URL" ]; then export VITE_REPO_URL="$REPO_URL"; fi \
 && pnpm --filter @veristar/site build \
 && test -f apps/site/dist/index.html && test -f apps/site/dist/404.html

FROM ${CADDY_IMAGE}
COPY --from=build /src/apps/console/dist /srv/console
COPY --from=build /src/apps/site/dist /srv/site
