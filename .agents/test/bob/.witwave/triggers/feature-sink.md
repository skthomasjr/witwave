---
name: feature-sink
description: Receives webhook deliveries from new-feature tests and echoes back the JSON body.
endpoint: feature-sink
enabled: true
---

A webhook delivery has arrived. The request body above is the payload. Respond with exactly:

FEATURE_SINK_OK
