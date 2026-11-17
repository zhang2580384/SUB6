# Ordinary WebSocket HTTP bridge continuation

Release base: formal SUB6 v0.1.9, commit
`60f23641079c0e7a6d672cc3faaa64b3ca9c9c5e`. No additional upstream range,
dependency, migration or operator setting is introduced.

## Reproduced problem

An ordinary native Responses client can send a second WebSocket create containing
only `previous_response_id` and new input after a successful `store=false` turn.
The previous bridge path rejected this local pointer at the encrypted recovery
gate. Its legacy replay collector also omitted delivered assistant explanation
when it retained a function call. Removing the pointer without restoring that
explanation would therefore continue with incomplete history.

Two independent localhost WS regressions preserve these failures before the
repair. A fictional provider creates an unpredictable code, so the bridge cannot
answer the continuation from the wording of either request. The continuation
must receive that exact code once, without a second handshake, hidden provider
retry, first-turn regeneration or completed-tool replay.

## Scope and safety checks

The bridge retains the latest fully completed readable response within one
authenticated logical WS session. Both explicit account HTTP bridge mode and
the existing threshold-selected bridge use this path. Native API-key and Codex
OAuth accounts are supported; managed credentials, native CN adapters,
passthrough, Copilot/SIWC, plugins, BPS/Prism, Responses Lite and smart stateful sessions are
excluded. Native upstream WS collection is unchanged.

A pointer can be removed only after a local proof restores the complete original
input and delivered output. The proof includes authenticated user/key/group,
original model, execution/thread scope, current account configuration, actual
upstream URL/authentication/identity headers/model/proxy and the current response
account binding. Current admission runs again. Source, prepared payload, projected
input and actual immutable HTTP body are checked before forwarding. A rebuilt
turn cannot use account-switch admission fallback, rejected-field retries or
automatic generation failover after the send boundary.

Only plain native messages, native function/custom calls (including existing
namespace projection), string tool results, and function results made solely of
ordered `input_text` parts are eligible. Image, file, mixed, unfamiliar and
duplicate-field parts are refused. Text is preserved without normalization. All earlier calls must have one matching completed result before
reconstruction. Full client history is recognized without adding it a second
time; partial overlapping assistant/tool history is refused. OAuth reserved tool
names are compared in client form and re-aliased for the provider.

Output is observed only after the actual downstream writer succeeds, after
client tool-name restoration. Ordered item completion, streamed text/refusal/tool
digests, item identities and final completion must agree. A terminal with an empty
output array can use fully delivered item-done events. Missing parts, conflicting
deltas/identities, unknown item fields or semantic events, nonempty annotations,
opaque reasoning/ciphertext/references, partial streams, cancellation and failed
delivery grant no proof. Existing protected-history ownership checks remain in
place; no lossy or readable-recovery Beta is enabled. Unknown external pointers
are refused with a structured WS error, without contacting a provider.

Each snapshot is limited to 2,048 items and an 8 MiB accounting budget. An atomic
128 MiB process-wide reservation budget covers retained snapshots, output copies
and conservative collector-metadata reservations. This is not a cap on total
process memory, ingress frames, request/HTTP buffers or temporary parsing copies.
The proof expires after the smaller of the sticky TTL and five minutes. Expired
storage is released on lookup or logical-session cleanup, rather than by a
five-minute wiping timer. Prompt/output bytes are not persisted by this cache or
written to diagnostic logs.

## Validation and limitations

The focused suite exercises explicit mode with its payload threshold disabled,
owner binding enabled/disabled, `store=false/true`, API-key/OAuth, omitted inherited
model, string input, complete client history, assistant-plus-tool continuation,
stream-only output, native namespace tools, the existing Lite seed path and the
OAuth reserved `python` name. The real-handler fixture
interleaves two independent logical conversations on one physical socket, in
both named-lane and legacy-thread forms. Each repeats its own random answer,
preserves the completed tool result and produces exactly one billing event per
turn. It runs against miniredis and real Redis on Linux.

Negative coverage includes principal/scope/model/account/credential/route/body/
binding changes, unknown/expired/new-socket pointers, ambiguous JSON, protected
input, output ordering and identity conflicts, missing/mismatched stream parts,
incomplete delivery, per-session/global limits and concurrent reservations.
Original failed evidence and the Issue805 ownership/tool assertions are retained.

The complete Linux suite also exposed an early candidate applying its replay
fence to ordinary fresh seeds. This broke existing rejected-field compatibility
retries and pre-generation account switching. The fence now applies to rebuilt
history turns; a fresh seed retains its current input for the existing failover
contract without accumulating legacy history. Original PR610 timing, Issue600
identity and moderation failover assertions remain intact and are included in
the race selection. A fictional WS regression checks both API-key and OAuth:
one rejected seed can retry and then continue its delivered answer, while a
rejected rebuilt history turn is never automatically sent again. The original
failed CI logs are preserved privately. A second red-to-green regression keeps
a completed reconstruction from disabling a later independent seed's retry;
the failure fence remains in place when a sent reconstruction fails.

```sh
go test -tags unit -p 2 ./internal/service ./internal/handler ./internal/repository \
  -run '^Test(Issue600|PR610|ModerationUpstream|Issue715|Issue758|Issue805|OpenAIAgent|OpenAIHTTPAndWSBridge|OpenAI409|OpenAI400|OpenAIWS.*(HTTPBridge|Recovery)|ProxyOpenAIWSHTTPBridge|WithoutOpenAI|OpenAIWSStream|OpenAIWSLogical)' \
  -count=1 -timeout=10m
```

Formal publication additionally requires the complete exact-SHA CI, real Redis
and race scenarios, embedded payment tests, PostgreSQL/migration/rollback tests,
frontend/backend builds, independent sidecar and clean distribution/image checks.
Windows Redis skips alone cannot satisfy these gates. Tree/history/asset scans
support manual review and do not establish absence of malicious code.

This is a bounded compatibility repair for proven readable local history. It
does not transfer encrypted provider state, span WS reconnections, retain every
historical response ID, probe another protocol, change compression or downgrade
to a non-streaming provider request. An unrecognized provider extension can still
make continuation ineligible. Real client/provider and environment acceptance
remain separate from fictional local regression evidence and source publication.

The live downstream lane binds provider response identity before usage observation.
An event ID or item ID alone is not a response ID. Pooled WS leases for a logical
connection are private to that instance and closed when released, including on
reconnect. This can increase connection churn; a connection-local upstream
continuation may require a fresh conversation. A downstream socket must reconnect
after 1,024 distinct response IDs. No response IDs are forgotten to make room.
Client error frames preserve recognized diagnostic codes, including `cyber_policy`,
without exposing upstream free text, partial output or metadata. Frames without a
response ID rely on a scoped transport, not independent proof of content origin.

For comparison of validated plaintext parts only, omitted `annotations` or
`logprobs` are equivalent to empty arrays. Delivered bytes, including their
original optional fields, are retained. Text, item IDs, part order and nonempty
metadata are not normalized. An output with encrypted reasoning is still
ineligible for this reconstruction; do not assume all clients or turns qualify.

On an HTTP-only bridge, a fresh explicit `response.create` with `generate:false`
now completes locally after admission. It returns an empty response ID, no output
and zero usage. It sends no generation to the provider and creates no usage row
or resumable history. The client sends its first real request with full input.
The normal Codex request for `reasoning.encrypted_content` as an output field is
allowed; an input containing actual opaque history, a continuation pointer or a
tool result is not a fresh prewarm. A late warmup returns a lane-local error and
leaves the prior real response available for continuation. A native WS provider
continues to handle its own protocol prewarm.
An inline plaintext prewarm message can carry a client item `id`. It is discarded
with that message and is never a provider response ID or a continuation anchor.

After an admitted passthrough `session.update`, the gateway may deliver a local
`session.updated` acknowledgment on that same logical connection. Unsolicited
acknowledgments are dropped. No provider metadata, usage or response identity
is copied into the notice. Long-context policy refusals have no invented
provider response ID. A bridge response-header timeout after upload does not
resend the request.
