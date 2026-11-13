# the production pin of node_exporter (infra/ansible), here so dependabot proposes
# updates for it. tests/pins fails until the ansible pin equals this line.
FROM prom/node-exporter:v1.12.1@sha256:1b4e4438faca4dd7e001dd445d161a4a2091b0fededa84093b3a8dfeae1f1be0
