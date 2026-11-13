# SUB6 deployment from reviewed source

The supported initial path is a local source build. No published SUB6 container image or prebuilt archive is claimed. Historical automatic installers are gated and exit without mutation.

1. Read the root README and maintenance review. Use a separate test environment first.
2. Create private local environment values for `POSTGRES_PASSWORD`, `JWT_SECRET`, `ADMIN_EMAIL` and `ADMIN_PASSWORD`. Use long randomly generated secrets, never commit them.
3. Inspect `compose.sub6.yml` and the root Dockerfile. Run `docker compose -f deploy/compose.sub6.yml build`, then `docker compose -f deploy/compose.sub6.yml up -d` only on the intended new/test host.
4. Visit the loopback port through your own authenticated access path. Configure TLS and trusted proxies before exposing it. Configure SMTP and a custom logo through the application's existing settings; never copy another installation's mail credentials or host overrides.

The example sets HTTP ingress, upstream non-stream response reads and client WS frames to 256 MiB. Model, text-endpoint and proxy limits can be lower. Large requests consume memory; set container and aggregate request-body budgets according to tested capacity. Existing WS/SSE proxy topologies and SMTP host/tunnel arrangements are deployment-specific, not embedded source defaults.

Do not run this example against a production data directory. PostgreSQL 18's data mount is `/var/lib/postgresql`. Preserve the exact original database image/data format when evaluating an existing installation. Application startup runs forward SQL migrations; a binary rollback does not reverse them.

The optional [direct-access sidecar](direct-access/README.md) needs its own restricted database roles, same-origin proxy and separately reviewed firewall reconciler. It is not automatically enabled by Compose. No script here creates DNS records, grants customer IP access or changes a customer's balance automatically.
