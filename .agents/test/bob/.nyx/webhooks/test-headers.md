---
name: test-headers
description: Verifies that custom headers (including {{env.VAR}} values) are sent with the webhook POST.
url: http://bob:8000/triggers/feature-sink
notify-when: on_success
notify-on-kind:
  - a2a
notify-on-response:
  - "*WEBHOOK_HEADERS_FIRE*"
content-type: application/json
headers:
  X-Test-Token: "{{env.WEBHOOK_TEST_TOKEN}}"
  X-Static-Header: static-value
body: |
  {"event": "headers-test", "kind": "{{kind}}", "agent": "{{agent}}"}
---
