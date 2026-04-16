---
name: test-env-url
description: Verifies that {{env.VAR}} interpolation works in the url field.
url: http://{{env.WEBHOOK_TEST_HOST}}/triggers/feature-sink
notify-when: on_success
notify-on-kind:
  - a2a
notify-on-response:
  - "*WEBHOOK_ENV_URL_FIRE*"
content-type: application/json
headers:
  Authorization: "Bearer {{env.WEBHOOK_TEST_BEARER}}"
body: |
  {"event": "env-url-test", "kind": "{{kind}}", "agent": "{{agent}}"}
---
