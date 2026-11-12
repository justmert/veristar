# shared settings of the local converge scripts (docs/deploy/local-converge.md).
# every name carries the sa-lc prefix so cleanup can find what was created.
LC_PREFIX=sa-lc
LC_STATE="$root/.scratch/scratch/local-converge"
LC_PUBLIC_NET=sa-lc-public
LC_PUBLIC_SUBNET=172.31.99.0/24
LC_PUBLIC_GATEWAY=172.31.99.1
LC_PRIVATE_NET=sa-lc-private
LC_PRIVATE_SUBNET=10.99.0.0/16
LC_REGISTRY=sa-lc-registry
LC_REGISTRY_IP=172.31.99.5
LC_REGISTRY_HOST_PORT=5099
LC_REGISTRY_IMAGE=registry:3.0.0@sha256:6c5666b861f3505b116bb9aa9b25175e71210414bd010d92035ff64018f9457e
LC_NODE_IMAGE=stellar-analytics/local-node:trixie
LC_IMAGE_TAG=lc
LC_ADMIN_USER=ops
# name public-address private-address, mirroring docs/deploy/opentofu.md
# section 2 (edge .11+index, core .21+index in the location's subnet)
LC_NODES="sa-lc-edge-1 172.31.99.12 10.99.1.12
sa-lc-edge-2 172.31.99.13 10.99.2.13
sa-lc-edge-3 172.31.99.14 10.99.3.14
sa-lc-core-1 172.31.99.22 10.99.1.22"
