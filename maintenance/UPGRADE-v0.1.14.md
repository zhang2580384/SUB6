# V4 channel monitor cache rates in 0.1.14

On each V4 group card, the high cache rate is the nearest-rank 90th percentile of completed intervals with business traffic. Hover over the value to see the interval size, sample count and the overall rate weighted by input tokens.

| Selected range | Interval | Intervals in range |
| --- | --- | --- |
| 90 minutes | 5 minutes | 18 |
| 24 hours | 1 hour | 24 |
| 7 days | 12 hours | 14 |
| 30 days | 1 day | 30 |

The card needs historical coverage back to the start of the selected range and at least ten completed, occupied intervals with valid cache rates. Zero rates count. Empty intervals, automatic checks and the interval still being aggregated are excluded. When coverage or samples are insufficient, the usual cache field applies. Public and admin V4 cards use the existing scoped matrix response.

In the V4 tab of the admin channel monitor, **Card sample display** controls the bars in both preview and customer cards. Select **Time windows** to divide the selected range into 18 equal slices, or **Latest 18 samples** to show up to 18 qualifying five-minute business intervals across the available history. V2 and V4 share this saved preference. The cache percentile always uses the selected range.

The release is based on SUB6 v0.1.13 (`96c30f3f0b9625f7cf3b263d7790d657ccd87ef4`). The V4 matrix can also request sample history according to the saved card setting. No new upstream commits, dependencies, database migrations or configuration are required. The existing monitor setting and billing policy continue to apply. Use the usual managed preflight and drain for upgrades. A binary rollback to v0.1.13 restores the previous display without a database restore.
