# the production pin of nats_exporter (infra/ansible), here so dependabot proposes
# updates for it. tests/pins fails until the ansible pin equals this line.
FROM natsio/prometheus-nats-exporter:0.20.3@sha256:4b8ddc1c86fec360b5cd3d5e1fbc6fb52bdafd5805bba3901cc52926538af255
