# the production pin of alertmanager (infra/ansible), here so dependabot proposes
# updates for it. tests/pins fails until the ansible pin equals this line.
FROM prom/alertmanager:v0.35.0@sha256:e638797febd034838eca8b98cdbd46f951e9bf7f178777aa8669167406a9946c
