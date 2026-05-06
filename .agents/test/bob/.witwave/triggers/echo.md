---
name: echo
description: Payload inspection trigger — reads the JSON body and echoes back the value of the "token" field.
endpoint: echo
enabled: true
---

The request body above contains a JSON payload. Read the value of the "token" field and respond with exactly:

ECHO:<token value>

For example, if the payload is {"token": "abc123"}, respond with ECHO:abc123. If the body is not valid JSON or the
"token" field is missing, respond with ECHO:MISSING.
