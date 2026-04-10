---
name: chain-test
description: Test webhook — fires at the webhook-sink trigger when response contains WEBHOOK_FIRE.
url: http://bob:8000/triggers/webhook-sink
notify-when: on_success
notify-on-kind:
  - a2a
notify-on-response:
  - "*WEBHOOK_FIRE*"
content-type: application/json
---
{"event": "webhook-chain-test", "kind": "{{kind}}", "session_id": "{{session_id}}"}
