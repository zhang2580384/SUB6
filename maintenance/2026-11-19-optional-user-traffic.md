# Optional request traffic metering

This local change is based on SUB6 `55258d8f74bc462d80abc6fc690cd19c513b17d8`; it imports no upstream commits and changes no schema, dependency, payment rule or packaging policy. Existing installations keep metering enabled. Disable it in **System settings → Usage records** to skip the byte-counting reader and writer on new gateway requests, preserve historical statistics and hide both traffic tabs. Direct report routes also enforce the setting. No billing record is deleted or suppressed.

The gateway refreshes its local switch asynchronously and may take about 30 seconds to see an edit made on another node. Requests already in progress and previous queued events finish under their original policy. Rollback to the previous application resumes the old always-on behavior. See [metering and coverage](../docs/user-traffic.md).
