# the production pin of etcd (infra/ansible), here so dependabot proposes
# updates for it. tests/pins fails until the ansible pin equals this line.
FROM quay.io/coreos/etcd:v3.7.4@sha256:641c8cb2492aedf4c693b20029a56f325f9b18754bb7dc9f6eac22c99786228c
