# Custom tool history after a tool catalog change

Base: SUB6 v0.1.10 (`a96340e090d707356efbe2eb987abfde64923344`), with the same reviewed upstream source (`aa8e596e6d6f3e4648d6beba73b39b240dd5357a`). This is a distribution fix; no upstream commits, migrations, settings or dependencies are added.

On a new WebSocket connection, a client may send its full Responses history without a `previous_response_id`. If a historical custom tool was absent from the new declarations, the function-tool adapter used to change its result to `function_call_output` while retaining `custom_tool_call`. A function-only upstream could no longer match the pair. An empty or omitted catalog also skipped adaptation of complete historical pairs.

The adapter now converts a historical call together with its matching output in the same input, even when the current declarations do not include it. Call IDs, tool input and supported output parts are preserved. Historical names are not offered as new tools. Without a current or inherited mapping, a lone output is left alone. No session lookup or tool execution is introduced.

Verification includes a real local WebSocket with a recording HTTP upstream: declared, removed, same name redeclared as a function, empty and absent declarations. Separate checks cover interleaved outputs, independent item IDs, missing or ambiguous matches, opaque nested fields and unchanged response mappings. Existing bridge continuation and isolation tests are retained. No production traffic or identifiers are included in these fixtures.

This fix can use the normal rolling update and rollback procedure. The prior release remains a compatible rollback target; rolling back restores the previous replay behavior.
