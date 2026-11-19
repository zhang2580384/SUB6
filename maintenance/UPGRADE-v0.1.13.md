# Upgrade from 0.1.12 to 0.1.13

Upgrading leaves the current site monitor mode in place. With the site on V2, open the channel monitor in the administrator panel, select the V4 tab and open the V4 preview. The preview does not change the customer page. To publish the view to customers, select V4 under **Site mode** and confirm. Select and confirm V2 in the same control to switch back immediately.

V4 retains the three metrics and eighteen five-minute slots. A group with sufficient recent traffic shows the 90th percentile of its completed windows; the tooltip also lists the measured rate across all input tokens. A recorded check can report connectivity in an idle slot; no cache percentage is inferred from success. Existing check schedules, billing and privacy controls remain in effect.

A Responses WebSocket now finishes an already accepted downstream write before closing a conversation. A client using a single legacy session receives the policy close code and reason; a client sharing a socket receives a scoped notice and can continue on its other streams. Locally refused models remain refused and are not reported as upstream account failures.

The migration fingerprint and controller protocol match 0.1.12. Use the normal managed preflight and drain. For a binary rollback, switch the site back to its previous monitor mode first. Probe percentages expire from Redis automatically; no database or balance changes must be reversed.
