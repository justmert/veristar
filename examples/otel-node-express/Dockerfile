# runs the express example on the pinned node image.
#   docker build -t otel-node-express examples/otel-node-express
# base image pinned by tag and index digest, so a rebuild is reproducible.

FROM node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc

ENV NODE_ENV=production
WORKDIR /app
# dependency install is its own layer so source edits do not reinstall
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --no-audit --no-fund && npm cache clean --force
COPY src ./src
USER node
EXPOSE 8080
# node runs as pid 1 directly (not through npm) so sigterm reaches the app
CMD ["node", "--require", "./src/telemetry.js", "src/server.js"]
