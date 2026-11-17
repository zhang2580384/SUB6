# Native HTTP and WebSocket HTTP bridge continuation

Publication base: `5c2c95943f48267e40880d24de9c5d54042b3ef5` (SUB6 v0.1.8).
The focused fix was first validated as `395cd064f9d903153583cda96848ca0ebefd4ad6`
and transferred onto the complete published release. It does not merge another
upstream range or change release versions.

## Problem and behavior

An API-key Responses request forwarded through the WS HTTP bridge was assigned
the synthetic `#agent-recovery-http_bridge` fingerprint namespace. Ordinary HTTP
ingress used no fragment, even when both emitted the same native HTTP request.
A completed encrypted-agent task could therefore fail the final-send ownership
check when the client changed its ingress protocol.

Continuation now recognizes these two representations only for native API-key
HTTP POST requests whose actual URL, identity headers and upstream model match.
Account identity/configuration, original encrypted items, provider response
anchors, current admission and delivery fences are still checked. Native upstream
WS modes, different adapters, OAuth and changed endpoints/credentials remain
separate. The actual body, headers and outbound URL are not rewritten by this fix.

Each task retains its exact original cached route. Fresh records keep their
existing format, and successful cross-ingress consumption publishes that same
route. No Redis clearing, ownership migration or last-writer-wins update is used.

Forwarding-stage WS refusals now send the existing structured recovery error
event before the close frame. The event carries the status and stable recovery
code; gateways can distinguish it from an upstream network failure. Diagnostic
logs include a bounded failure stage without request content, credentials or
fingerprints. Downstream clients still control their own error rendering.

## Validation

The pre-fix implementation fails the HTTP/bridge transition regression and closes
the forwarding-stage refusal without an error event. Focused fixtures use
fictional task/tool data and localhost providers; they do not prove that a live
provider will decrypt an existing task.

Coverage includes both ingress directions through real handlers and ownership
Lua, legacy bridge records, repeated transitions, unchanged original publication,
changed account/authorization/organization/project/features/URL/query/model,
modified encrypted items, native WS isolation and full WS error framing.

Validation completed with Go 1.27.4:

- Focused Windows service, handler and repository tests passed. Unix-socket
  Redis tests skipped on Windows were subsequently executed on Linux.
- The Linux handler suite passed with real isolated Redis, zero failed and zero
  skipped tests, including both new handler scenarios on real Redis. The fixture
  had no external network or production data access and was removed afterwards.
- The Linux/amd64 backend build and `git diff --check` passed.
- New regressions are included in the existing CI race-test selection. Local
  checks did not run `-race`; this remains a release CI gate.

Run the relevant regressions with a real `redis-server` available:

```sh
go test -tags unit -p 2 ./internal/service ./internal/handler ./internal/repository \
  -run '^Test(Issue715|Issue758|Issue805|OpenAIAgent|OpenAIHTTPAndWSBridge|OpenAI409|OpenAI400|OpenAIWS.*(HTTPBridge|Recovery)|WithoutOpenAI)' \
  -count=1 -timeout=10m
```

## Upgrade and rollback

No schema migration, setting or dependency change is required. Build and publish
through the reviewed release process before a separately authorized production
upgrade. Rollback retains the original cache representation; an older binary
will again reject cross-ingress continuation it previously could not admit.

This patch addresses an independently reproduced compatibility defect. A past
incident whose logs contain only `wire_changed` cannot retrospectively be assigned
to one specific changed field. It does not make a real upstream account change
or unknown encrypted history portable, and does not automatically replay a
generation or an already completed tool action.
