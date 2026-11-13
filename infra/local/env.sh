# shared settings of the local converge scripts (docs/deploy/local-converge.md).
# every name carries the sa-lc prefix so cleanup can find what was created.
LC_PREFIX=sa-lc
LC_STATE="$root/.scratch/scratch/local-converge"
LC_PUBLIC_NET=sa-lc-public
LC_PUBLIC_SUBNET=172.31.99.0/24
LC_PUBLIC_GATEWAY=172.31.99.1
# the private network spans three locations (docs/deploy/opentofu.md section
# 2). each location is its own docker network, and one router container
# joins them, so traffic between locations crosses the router, where tc
# netem adds the latency and a partition cuts a location off
# (docs/deploy/local-converge.md section 6).
LC_PRIVATE_SUBNET=10.99.0.0/16
LC_LOCATIONS="fsn1 10.99.1
nbg1 10.99.2
hel1 10.99.3"
LC_ROUTER=sa-lc-router
# one-way delay the router adds toward each location, in ms. the round trip
# between two locations is the sum of both; these are assumptions standing
# in for hetzner's private network, not measurements of it an operator input
LC_DELAY_fsn1=1.5
LC_DELAY_nbg1=1.5
LC_DELAY_hel1=11.5
LC_JITTER_MS=0.3
LC_REGISTRY=sa-lc-registry
LC_REGISTRY_IP=172.31.99.5
LC_REGISTRY_HOST_PORT=5099
LC_REGISTRY_IMAGE=registry:3.0.0@sha256:6c5666b861f3505b116bb9aa9b25175e71210414bd010d92035ff64018f9457e
LC_NODE_IMAGE=stellar-analytics/local-node:trixie
LC_IMAGE_TAG=lc
LC_ADMIN_USER=ops
# name location public-address private-address, mirroring
# docs/deploy/opentofu.md section 2 (edge .11+index, core .21+index in the
# location's subnet). the backup node holds the pgbackrest repository in a
# location without a database node, standing in for object storage.
LC_NODES="sa-lc-edge-1 fsn1 172.31.99.12 10.99.1.12
sa-lc-edge-2 nbg1 172.31.99.13 10.99.2.13
sa-lc-edge-3 hel1 172.31.99.14 10.99.3.14
sa-lc-core-1 fsn1 172.31.99.22 10.99.1.22
sa-lc-core-2 nbg1 172.31.99.23 10.99.2.23
sa-lc-backup-1 hel1 172.31.99.31 10.99.3.31"
# the fresh node a point-in-time restore goes to; created by the restore
# drill only (tests/drills/pitr.sh) and removed after it
LC_RESTORE_NODE="sa-lc-restore-1 fsn1 172.31.99.41 10.99.1.41"
