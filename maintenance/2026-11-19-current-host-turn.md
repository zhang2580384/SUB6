# Current Codex host turns

The HTTP Responses entrance serializes only a final standalone Codex delegation or scheduled event as a message. It keeps the entire host envelope, its source and existing permission semantics. Earlier events remain in the transcript; ordinary tool results still require their original identities and a response continuation still requires its original owner. No prompt, model tool, scheduler or WebSocket setting changes.

Reviewed HTTP ingress, tool pairing and the native named-output preservation path at Wei-Shaw `v0.2.23` (`5af9afb588c4b014b3df9db7d4073af1f8ae01b6`) and `main` (`2af3f1e15c98c893a89255a037f551046883b8c0`), and Ranxi `v2.22.0` (`4ba33f8713cbf6c6406e0f2639ffcc2ef1e31c87`) and `production` (`e687827fcb5c2bbb347dfb1d15f116c0ab0580c9`). Those paths retain separate automation/delegation scans and the automation continuation exclusion. This change does not merge upstream commits.

The local HTTP fixtures distinguish a delivered current task from replayed history; they cannot establish that any specific model will choose to use a tool. Rolling back the application restores the old translation. No migration, billing or authorization change is required.
