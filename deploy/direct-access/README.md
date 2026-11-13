# SUB6 direct access sidecar

An optional Flask management service for explicit IP allowlists. It uses the
main gateway's `/api/v1/auth/me` as the authentication authority and keeps its
own PostgreSQL schema. It is not started by the main application or source
Compose. This repository contains no host installer or production configuration.

## Behavior

Each $100 of effective cumulative credit grants one concurrent IP slot, capped
at 100. Successful `balance` redemptions and positive `admin_balance` credits
count; remaining balance does not determine eligibility. The larger of the
legacy cumulative counter and successful credit history is used, avoiding double
counting and preserving imported historical totals. Administrators can replace
the automatic total with an audited override from 0 to 100, or reset it to auto.
Reducing quota removes excess entries from the eligible view without deleting
customer records. This service never changes gateway balances or billing.

The network allowlist is shared across eligible customers. It does not bind a
particular API key to the owner of an IP. Main API-key checks, account status,
per-key restrictions, routing and billing remain necessary. Existing streams
are not forcibly disconnected on an IP removal.

## Configuration and database permissions

Supply `DATABASE_URL`, `AUTH_URL`, `DIRECT_URL`, and comma-separated
`ALLOWED_ORIGINS`. Missing required values stop startup; no deployment address is
built in. Copy `.env.example` into a private local environment file, or set
`DIRECT_ACCESS_CONFIG_FILE=/etc/sub6-direct/settings.json` and copy
`settings.example.json` there. The JSON file supplies the same uppercase keys;
explicit environment values override the file. Unknown JSON keys fail startup.
Keep configuration outside this repository, owned by the operator and readable
only by the service account (for example root:sub6-direct, mode 0640). Never give
the web process write access to its configuration.

Prefer a stable HTTPS gateway URL such as
`https://gateway.example.com/api/v1/auth/me`. Do not pin an ephemeral release
container IP: replacing that container can leave authentication pointing at a
stopped instance. AUTH_URL must use exactly `/api/v1/auth/me`; only numeric
loopback addresses permit HTTP. Redirect responses are rejected so a bearer
token cannot follow a redirect to another destination. Outbound auth bypasses
process proxy variables. HTTPS certificate validation remains enabled.

Use exact HTTPS site origins without paths or wildcards. Never put tokens in iframe URLs.
Route `/direct-access/` on the authenticated site's origin; mutations require an
exact allowed Origin and a bearer token. Admin operations recheck the main
account's admin role. The initial iframe redirect accepts only configured
origins and forwards only the display theme.

`BRAND_NAME`, `PAGE_TITLE`, `PAGE_DESCRIPTION`, and `ENTRY_DESCRIPTION` customize
the page as escaped plain text. They default to SUB6 and generic Chinese copy.
The direct endpoint comes from `DIRECT_URL`. These are deployment settings;
the customer IP/quota panel does not edit them. The main application's Settings
page links to this guide. Administrators may inspect the safe, read-only
`GET /direct-access/api/admin/config` response; it omits AUTH_URL and database
credentials. There is no browser API to change the authentication authority.

A database administrator must review and apply `schema.sql` transactionally to
a test copy first. The schema refers to the main `users` and `redeem_codes`
tables; views are owned by that administrator. Create a separate login for this
service and grant only USAGE on `direct_access`, SELECT on its views, and the
specific table/sequence rights required by `app.py`. Do not grant this login
access to public credential tables. Give a separate synchronizer role SELECT
on `eligible_entries` and UPDATE on `sync_state`; do not reuse the database
administrator login. Schema installation and grants are deliberate deployment
operations, not application startup hooks.

Build the supplied Dockerfile or install `requirements.lock` in a dedicated
Python environment. Bind the management listener to loopback. The container
must not receive privileged mode, host networking or firewall access. Mount no
main gateway secrets. Configure the edge proxy separately and verify both API
and WebSocket paths before enabling customer traffic.

`sub6-direct.service.example` is an operator-reviewed systemd example, using a
dedicated account and environment file. Its paths are placeholders. Do not use
Gunicorn `--preload`: every new worker must import and validate configuration.
After editing the JSON file or code, `systemctl reload sub6-direct` gracefully
replaces workers and rereads that file. **Gunicorn HUP does not reread a systemd
EnvironmentFile.** Changing AUTH_URL or other environment values requires a
controlled restart of this independent service, or moving the value to JSON
and removing the overriding environment value before that restart. Editing the
env file and sending HUP alone leaves the master's old environment in effect.
Restart/reload this sidecar only; no main gateway restart is required.

## Edge and firewall integration

`reconcile.py` is an optional Linux/firewalld synchronizer requiring separate
operator review and privileged scheduling. It owns only exact-source TCP/443
accept rules at priority -35 in zone public. It neither flushes other rules nor
opens the port globally. It validates canonical public IPs and records a
snapshot only after verifying the live rules. Do not run it automatically from
a build, CI or this application's container.

The edge must send its verified TCP peer as `X-Direct-Peer` only to the
loopback `/internal/check` endpoint and strip spoofed client values. The service
fails closed when the applied snapshot is missing, stale or unavailable. Trusted
proxy and certificate configuration is deployment-specific. No ready-to-run
firewall or reverse-proxy configuration is supplied here.

## Validation and rollback

`test_policy.py` exercises canonical IP and firewall-rule validation.
`test_configuration.py` verifies required values, trusted auth paths, exact
origins and operator-file/environment precedence without a database or network.
`test_access.py` uses only an explicitly named disposable loopback PostgreSQL
database (`sub6_review`); it replaces fixture tables and must never use a gateway
database. Run `python -m unittest test_policy test_configuration test_access -v` with
`SUB6_REVIEW_DATABASE_URL` set to that fixture. CI supplies a disposable service.

Before deployment, preserve schema definitions, sidecar code, private local
configuration and separately managed edge/firewall state. Disable the edge
entry point and stop the synchronizer before reverting sidecar versions. Do not
drop the schema while it is in use; no automated production rollback is included.

For a code/configuration-only update from 0.1.1, `schema.sql` is unchanged. Keep
existing entries, quota overrides, events, database grants and synchronizer
state. Back up the code and private configuration, validate a disposable copy,
then reload/restart only the sidecar as described above. Check the new workers'
startup logs, verify no-token and forged-token requests return 401, and use an
existing real login to refresh entries with a read-only GET. Confirm the same
entries and quotas remain visible and a previous auth error clears after a
successful refresh. These checks must not create/delete IPs or alter quotas.
If validation fails, restore the backed-up sidecar code/configuration and apply
the corresponding reload/restart. Do not revert the main application's data.
