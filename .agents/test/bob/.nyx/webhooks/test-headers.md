---
name: test-headers
description: Verifies that custom headers (including {{env.VAR}} values) are sent with the webhook POST.
url: http://{{env.WEBHOOK_TEST_HOST}}/triggers/feature-sink
notify-when: on_success
notify-on-kind:
  - a2a
notify-on-response:
  - "*WEBHOOK_HEADERS_FIRE*"
content-type: application/json
headers:
  Authorization: "Bearer {{env.WEBHOOK_TEST_BEARER}}"
  X-Test-Token: "{{env.WEBHOOK_TEST_TOKEN}}"
  X-Static-Header: static-value
body: |
  {"event": "headers-test", "kind": "{{kind}}", "agent": "{{agent}}"}
---
