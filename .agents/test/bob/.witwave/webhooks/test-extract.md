---
name: test-extract
description: Verifies that LLM extraction runs and produces variables available in the body template.
url-env-var: WEBHOOK_TEST_URL_FEATURE_SINK
notify-when: on_success
notify-on-kind:
  - a2a
notify-on-response:
  - "*WEBHOOK_EXTRACT_FIRE*"
content-type: application/json
headers:
  Authorization: "Bearer {{env.WEBHOOK_TEST_BEARER}}"
extract:
  extracted_word: Read the text below and return only the word that appears between the tokens EXTRACT_START and EXTRACT_END. Return that single word and nothing else.
body: |
  {"event": "extract-test", "extracted": "{{extracted_word}}", "agent": "{{agent}}"}
---

{{response_preview}}
