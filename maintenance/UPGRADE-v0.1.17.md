# SUB6 0.1.17

Claude Chat Completions and Responses now select the image or document representation from its media type. Native Claude routes validate inline media before forwarding it. Images, PDF and UTF-8 text are preserved in user messages and structured tool results. Plain tool text keeps its original meaning. Gemini and Antigravity keep their own media rules.

The DeepSeek API key Chat route accepts an upstream stream that joins a known empty preamble directly to the assistant start record. Properly separated events, normal multi-line events, later usage and terminal records keep their existing behavior.

Antigravity shows a reset deadline when quota has been used, including usage below one percent. An untouched quota has no active countdown. The Astra reissue regression derives both ticket timestamps from one clock reading so the assertion holds across a second boundary.

These changes port Ranxi [#984](https://github.com/ranxi2001/sub2api/pull/984), [#993](https://github.com/ranxi2001/sub2api/pull/993), [#994](https://github.com/ranxi2001/sub2api/pull/994) and the test change in [#998](https://github.com/ranxi2001/sub2api/pull/998). The source commits are listed in `upstream.lock.json`.

Public 0.1.16 and 0.1.17 share the same 421 migration files and dependency manifests. For the public installation, verify `checksums.txt` and run the managed updater preflight against the installed fingerprint. An installation with custom migrations needs a build from its own source line. The previous public binary can be used for application rollback when the schema and installation match.
