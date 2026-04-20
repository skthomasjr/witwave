---
name: webhook
description: HMAC-authenticated webhook trigger — validates X-Hub-Signature-256 using BOB_WEBHOOK_SECRET.
endpoint: webhook
enabled: true
secret-env-var: BOB_WEBHOOK_SECRET
---

A webhook event has arrived. Parse the request body as JSON and summarize the event type and key fields.
If the body is not JSON, describe the raw payload.
