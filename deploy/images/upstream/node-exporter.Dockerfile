# the production pin of node_exporter (infra/ansible), here so dependabot proposes
# updates for it. tests/pins fails until the ansible pin equals this line.
FROM prom/node-exporter:v1.13.1@sha256:7296504d4ada44b7d7ab4431e27e9f9900d8488f8d55842f4b12ae2704e16dfe
