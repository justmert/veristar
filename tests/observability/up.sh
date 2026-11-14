#!/bin/sh
# brings up the observability local TEST STACK (compose.yml): builds the platform
# binaries, creates test certificates and secrets under
# .scratch/p17, renders the observability role's templates with
# render.yml, starts everything, signs in as the fixture operator, creates
# the probe tenant with platformctl and starts the prober.
#   tests/observability/up.sh      start or update
#   tests/observability/down.sh    remove containers, volumes and images
set -eu
root="$(cd "$(dirname "$0")/../.." && pwd)"
P17="$root/.scratch/p17"
export P17
cd "$root/tests/observability"
export PATH="$HOME/.local/go1.27.4/bin:$HOME/.local/bin:$PATH"
ansible_playbook="$root/.scratch/ansible-venv/bin/ansible-playbook"
compose() { docker compose -f "$root/tests/observability/compose.yml" "$@"; }

mkdir -p "$P17/bin" "$P17/env" "$P17/conf" "$P17/certs"
for n in edge-1 edge-2 edge-3 core-1 core-2; do mkdir -p "$P17/textfile/$n"; done

echo "== binaries"
(cd "$root" && CGO_ENABLED=0 go build -trimpath -o "$P17/bin/" ./cmd/gateway ./cmd/replayer ./cmd/metricswriter ./cmd/jobs ./cmd/platformctl ./cmd/prober ./cmd/sloreport)
(cd "$root" && CGO_ENABLED=0 go build -trimpath -tags oauthfixture -o "$P17/bin/controlplane" ./cmd/controlplane)
(cd "$root" && CGO_ENABLED=0 go build -trimpath -o "$P17/bin/" ./tests/fixtures/githuboauth/cmd/githuboauth-fixture ./tests/observability/receiver ./tests/observability/drill)

echo "== certificates"
[ -f "$P17/certs/ca.crt" ] || sh ./certs.sh "$P17/certs"

echo "== secrets"
rand() { od -An -tx1 -N"$1" /dev/urandom | tr -d ' \n'; }
b64() { head -c "$1" /dev/urandom | base64 | tr -d '\n'; }
umask 077
if [ ! -f "$P17/env/base.env" ]; then
	cat >"$P17/env/base.env" <<EOF
PG=$(rand 24)
UMAMI_DB=$(rand 24)
UMAMI_APP=$(rand 32)
UMAMI_ADMIN=$(rand 24)
CP_SYSTEM=$(rand 24)
CP_TENANT=$(rand 24)
CP_OPERATOR=$(rand 24)
MW=$(rand 24)
JOBS=$(rand 24)
MONITOR=$(rand 24)
GRAFANA=$(rand 24)
STATE_KEY=$(b64 32)
PW_KEY=$(b64 32)
GATUS_TOKEN=$(rand 24)
METRICS_TOKEN=$(rand 24)
EOF
fi
. "$P17/env/base.env"
cat >"$P17/env/stack.env" <<EOF
TZ=UTC
POSTGRES_PASSWORD=$PG
UMAMI_DB_PASSWORD=$UMAMI_DB
NATS_URL=nats://172.30.17.21:4222,nats://172.30.17.22:4222,nats://172.30.17.23:4222
NATS_REPLICAS=3
PLATFORM_DB_HOST=postgres
PLATFORM_DB_PORT=5432
PLATFORM_DB_NAME=platform
PLATFORM_DB_SSLMODE=disable
PLATFORM_DB_ADMIN_USER=postgres
PLATFORM_DB_ADMIN_PASSWORD=$PG
PLATFORM_DB_CP_SYSTEM_PASSWORD=$CP_SYSTEM
PLATFORM_DB_CP_TENANT_PASSWORD=$CP_TENANT
PLATFORM_DB_CP_OPERATOR_PASSWORD=$CP_OPERATOR
PLATFORM_DB_METRICS_WRITER_PASSWORD=$MW
PLATFORM_DB_JOBS_PASSWORD=$JOBS
UMAMI_URL=http://umami:3000
UMAMI_ADMIN_PASSWORD=$UMAMI_ADMIN
UMAMI_USER_PASSWORD_KEY=$PW_KEY
OAUTH_STATE_KEY=$STATE_KEY
GITHUB_CLIENT_ID=p17-fixture-client
GITHUB_CLIENT_SECRET=p17-fixture-secret
GITHUB_REDIRECT_URL=https://app.p17.test/api/auth/github/callback
OAUTH_FIXTURE_URL=http://oauth-fixture:8099
OPERATOR_GITHUB_IDS=1001
CONTROLPLANE_ADDR=0.0.0.0:8085
CONTROLPLANE_OPS_ADDR=0.0.0.0:9090
CONSOLE_ORIGIN=https://app.p17.test
INGEST_ORIGIN=https://in.p17.test
WEB_ORIGIN=https://web.p17.test
TRACKER_RELEASE_DIR=/releases
API_MINUTE_RETENTION_DAYS=30
API_HOURLY_RETENTION_MONTHS=36
GATEWAY_ADDR=0.0.0.0:8080
GATEWAY_OPS_ADDR=0.0.0.0:9090
PROOF_BITS=12
PROOF_REPLAY_LIMIT=50
IPCLUSTER_THRESHOLD=20
VISITOR_EVENTS_PER_HOUR=600
RATE_SITE_PER_SECOND=200
RATE_SITE_BURST=400
RATE_PREFIX_PER_SECOND=20
RATE_PREFIX_BURST=60
GATEWAY_MAX_BODY=8192
CLIENT_IP_HEADER=X-Edge-Client-Ip
GATEWAY_SECURE_TRANSPORT=true
DBIP_MMDB=/geo/dbip-country-lite.mmdb
REPLAYER_OPS_ADDR=0.0.0.0:9090
REPLAYER_WORKERS=16
REPLAYER_MAX_IN_FLIGHT=512
METRICSWRITER_OPS_ADDR=0.0.0.0:9090
METRICSWRITER_BATCH=256
METRICSWRITER_FIRST_POINT_THRESHOLD=120s
METRICSWRITER_ROUTE_CAP=300
METRICSWRITER_SERVICE_CAP=50
JOBS_OPS_ADDR=0.0.0.0:9090
EOF
printf 'DATABASE_URL=postgresql://umami_app:%s@postgres:5432/umami\nAPP_SECRET=%s\n' "$UMAMI_DB" "$UMAMI_APP" >"$P17/env/umami.env"
printf 'GF_SECURITY_ADMIN_USER=admin\nGF_SECURITY_ADMIN_PASSWORD=%s\n' "$GRAFANA" >"$P17/env/grafana.env"
printf 'DATA_SOURCE_PASS=%s\n' "$MONITOR" >"$P17/env/pgexp.env"
printf 'PROBE_METRICS_TOKEN=%s\n' "$METRICS_TOKEN" >"$P17/env/probe-caddy.env"
printf 'GATUS_DISCORD_WEBHOOK_URL=http://receiver:8099/webhook/gatus\nGATUS_EXTERNAL_TOKEN=%s\n' "$GATUS_TOKEN" >"$P17/env/gatus.env"
write_secrets() {
	{
		echo "sa_secrets:"
		echo "  probe_metrics_token: \"$METRICS_TOKEN\""
		echo "  gatus_external_token: \"$GATUS_TOKEN\""
		if [ -f "$P17/env/probe-tenant.out" ]; then
			sed -n 's/^site_id=\(.*\)/  probe_site_id: "\1"/p; s/^otlp_token=\(.*\)/  probe_otlp_token: "\1"/p; s/^machine_api_key=\(.*\)/  probe_machine_api_key: "\1"/p' "$P17/env/probe-tenant.out"
		fi
	} >"$P17/secrets.yml"
}
write_secrets
umask 022
chmod 0644 "$P17"/env/*.env

echo "== caddyfiles"
# the assembled node caddyfiles of deploy/caddy, plus one test-only tls
# snippet: SA_TLS=files loads the test certificate of the node
snippet='(sa_tls_files) {
	tls /certs/node.crt /certs/node.key
}
'
{ printf '%s\n' "$snippet"; sh "$root/deploy/caddy/assemble.sh" edge "$root/deploy/caddy/edge.Caddyfile"; } >"$P17/conf/edge.Caddyfile"
{ printf '%s\n' "$snippet"; sh "$root/deploy/caddy/assemble.sh" core "$root/deploy/caddy/app.Caddyfile" "$root/deploy/caddy/web.Caddyfile"; } >"$P17/conf/core.Caddyfile"

render() { ANSIBLE_ROLES_PATH="$root/infra/ansible/roles" "$ansible_playbook" -i inventory.yml render.yml -e "p17=$P17" -e "observability_repo_root=$root" >"$P17/render.log" 2>&1 || { cat "$P17/render.log"; exit 1; }; }
echo "== render"
render

echo "== infrastructure"
compose up -d --wait postgres node-edge-1 node-edge-2 node-edge-3 node-core-1 node-core-2
compose up -d nats-1 nats-2 nats-3 etcd-1 etcd-2 etcd-3
compose up -d --wait umami

ctl() { docker run --rm --network sa-p17 --user 65534:65534 --env-file "$P17/env/stack.env" -v "$P17/bin:/bin-sa:ro" debian:trixie "$@"; }
echo "== platform database, nats topology, umami"
ctl /bin-sa/platformctl db migrate
ctl /bin-sa/platformctl db roles
ctl /bin-sa/platformctl nats apply
ctl /bin-sa/platformctl umami bootstrap
# postgres_exporter's role: statistics only
docker exec -i sa-p17-postgres-1 psql -U postgres -d postgres -v ON_ERROR_STOP=1 -v "pw=$MONITOR" >/dev/null <<'SQL'
select format(case when exists (select 1 from pg_roles where rolname = 'sa_monitor')
  then 'alter role sa_monitor login password %L'
  else 'create role sa_monitor login password %L in role pg_monitor' end, :'pw') \gexec
SQL

echo "== services"
compose up -d --remove-orphans $(compose config --services | grep -Ev '^(prober|gatus|caddy-probe)$')

if [ ! -f "$P17/env/probe-tenant.out" ]; then
	echo "== operator sign-in through the oauth fixture, then the probe tenant"
	docker run --rm --network sa-p17 --user 65534:65534 -v "$P17:/p17:ro" \
		--add-host app.p17.test:172.30.17.31 debian:trixie /p17/bin/drill signin -ca /p17/certs/ca.crt
	umask 077
	ctl /bin-sa/platformctl probe-tenant create -operator-github-id 1001 -domain probe.p17.test -origin https://probe.p17.test >"$P17/env/probe-tenant.out"
	umask 022
	# a new jobs leader runs every task at once, reconcile included, which
	# provisions the umami websites and publishes the registries now
	# instead of at the next 10 minute reconcile
	compose restart jobs-1 jobs-2
fi
write_secrets
render
compose up -d --force-recreate gatus caddy-probe
cp "$P17/conf/probe-1/prober.env" "$P17/env/prober.env"
compose up -d prober
echo "up: prometheus http://127.0.0.1:19090 alertmanager http://127.0.0.1:19093 grafana http://127.0.0.1:13000 gatus http://127.0.0.1:18080 receiver http://127.0.0.1:18099/messages"
