---
name: test-env-url
description: Verifies that env-derived URLs resolve correctly via the url-env-var field.
url-env-var: WEBHOOK_TEST_URL_FEATURE_SINK
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
