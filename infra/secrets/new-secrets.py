#!/usr/bin/env python3
"""writes the generated secrets of one environment as plain yaml on stdout.

    python3 infra/secrets/new-secrets.py <env> | sops encrypt --filename-override \
        infra/secrets/<env>/secrets.enc.yaml --input-type yaml --output-type yaml /dev/stdin \
        > infra/secrets/<env>/secrets.enc.yaml

docs/deploy/secrets.md describes the whole procedure. every value here is
random and generated on the spot. values that come from the operator (inputs I2
and I6) are never generated: they are taken from the environment variables
named below and left out when unset, and the ansible secrets role then
refuses to converge a node that needs a missing one. so the file never holds
a placeholder that looks like a credential.

nats needs a bcrypt hash per user for the server and the plain password for
the client; both are generated together here, because a hash computed during
every converge would change the server config each run.
"""

import os
import secrets
import sys

import bcrypt
import yaml

NATS_USERS = ["gateway", "collector", "replayer", "metricswriter", "jobs", "controlplane", "platformctl"]

# owner inputs: variable name in this script's environment -> key in the file
INPUTS = {
    "SA_GITHUB_CLIENT_ID": "github_client_id",  # I2
    "SA_GITHUB_CLIENT_SECRET": "github_client_secret",  # I2
    "SA_CLOUDFLARE_API_TOKEN": "cloudflare_api_token",  # I6, caddy dns-01
    # the local converge's s3 stand-in only; azure backups use the core
    # nodes' managed identity and have no key
    "SA_OBJECT_STORAGE_ACCESS_KEY": "object_storage_access_key",
    "SA_OBJECT_STORAGE_SECRET_KEY": "object_storage_secret_key",
    "SA_ALERTMANAGER_DISCORD_WEBHOOK_URL": "alertmanager_discord_webhook_url",  # I8, alerts
    "SA_GATUS_DISCORD_WEBHOOK_URL": "gatus_discord_webhook_url",  # I8, status page alerts
}


def hexkey(n: int) -> str:
    # hex keeps database urls free of characters that need escaping
    return secrets.token_hex(n)


def b64key(n: int) -> str:
    # the control plane keys are 32 random bytes in standard base64
    import base64

    return base64.b64encode(secrets.token_bytes(n)).decode()


def bcrypt_hash(pw: str) -> str:
    # nats-server accepts $2a$ hashes; cost 11 matches the nats tooling default
    return bcrypt.hashpw(pw.encode(), bcrypt.gensalt(rounds=11, prefix=b"2a")).decode()


def main() -> int:
    if len(sys.argv) != 2 or sys.argv[1] not in ("local", "staging", "production"):
        print("usage: new-secrets.py local|staging|production", file=sys.stderr)
        return 2
    nats = {u: hexkey(24) for u in NATS_USERS}
    route = hexkey(24)
    doc = {
        "sa_secrets_environment": sys.argv[1],
        "postgres_superuser_password": hexkey(24),
        "postgres_replication_password": hexkey(24),
        "patroni_restapi_password": hexkey(24),
        "umami_db_password": hexkey(24),
        "umami_app_secret": hexkey(32),
        "umami_admin_password": hexkey(24),
        "umami_user_password_key": b64key(32),
        "umami_db_jobs_retention_password": hexkey(24),
        # pgbackrest encrypts the repository with it (aes-256-cbc), so the
        # object storage provider never holds readable backups
        "pgbackrest_cipher_pass": hexkey(32),
        "oauth_state_key": b64key(32),
        "platform_db_cp_system_password": hexkey(24),
        "platform_db_cp_tenant_password": hexkey(24),
        "platform_db_cp_operator_password": hexkey(24),
        "platform_db_metrics_writer_password": hexkey(24),
        "platform_db_jobs_password": hexkey(24),
        "nats_passwords": nats,
        "nats_password_hashes": {u: bcrypt_hash(p) for u, p in nats.items()},
        "nats_route_password": route,
        "nats_route_password_hash": bcrypt_hash(route),
        # monitoring (roles/observability, the design)
        "grafana_admin_password": hexkey(24),
        "postgres_exporter_password": hexkey(24),
        # prometheus on the core nodes reads the probe node's metrics with it
        "probe_metrics_token": hexkey(32),
        "gatus_external_token": hexkey(32),
    }
    for var, key in INPUTS.items():
        v = os.environ.get(var, "")
        if v:
            doc[key] = v
        else:
            print(f"new-secrets.py: {var} unset, {key} left out", file=sys.stderr)
    yaml.safe_dump(doc, sys.stdout, sort_keys=False)
    return 0


if __name__ == "__main__":
    sys.exit(main())
