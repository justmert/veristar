# the production pin of etcd (infra/ansible), here so dependabot proposes
# updates for it. tests/pins fails until the ansible pin equals this line.
FROM quay.io/coreos/etcd:v3.7.3@sha256:8d1b6c24f08e12ed7623d1df421832b3cafabe221aa6b658b97288d07807582f
