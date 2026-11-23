# the production pin of haproxy (infra/ansible), here so dependabot proposes
# updates for it. tests/pins fails until the ansible pin equals this line.
FROM haproxy:3.4.8-alpine@sha256:8aa6458a9d22a8364a2d2ee9ce93de0cc4a2789e3cf63f0b44adc972863724d7
