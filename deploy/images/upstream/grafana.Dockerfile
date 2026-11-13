# the production pin of grafana (infra/ansible), here so dependabot proposes
# updates for it. tests/pins fails until the ansible pin equals this line.
FROM grafana/grafana:13.2.6@sha256:b21409d7951ddcfd63a4b49083e4ef2cc35bc1437d071813bf82bfb238b00bca
