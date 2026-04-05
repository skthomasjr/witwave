---
name: remote
description: Send a prompt to a named remote agent via A2A and return the response
argument-hint: "<agent-name> <prompt>"
---

Send a prompt to a named remote agent. The first word of the arguments is the agent name, the rest is the prompt.

**Arguments:** $ARGUMENTS

Use the Bash tool to:

1. Parse the agent name and prompt from the arguments (first word = agent name, remainder = prompt)
2. Read `docker-compose.yml` in the current directory to find the host port mapped to that agent's service
3. Derive the current session ID from the most recently modified `.jsonl` file in the Claude project directory
4. Send the prompt via A2A JSON-RPC to the correct port

```bash
ARGS="$ARGUMENTS"
AGENT_NAME=$(echo "$ARGS" | awk '{print $1}')
PROMPT=$(echo "$ARGS" | cut -d' ' -f2-)
PORT=$(grep -A5 "^  ${AGENT_NAME}:" docker-compose.yml | grep -o '"[0-9]*:8000"' | cut -d'"' -f2 | cut -d':' -f1)
PROJECT_PATH=$(pwd | sed 's|^/||' | tr '/' '-')
SESSION_ID=$(ls -t ~/.claude/projects/${PROJECT_PATH}/*.jsonl 2>/dev/null | head -1 | xargs basename -s .jsonl)
MESSAGE_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')

curl -s -X POST http://localhost:${PORT}/ \
  -H "Content-Type: application/json" \
  -d "{
    \"jsonrpc\": \"2.0\",
    \"method\": \"message/send\",
    \"id\": 1,
    \"params\": {
      \"message\": {
        \"messageId\": \"${MESSAGE_ID}\",
        \"role\": \"user\",
        \"metadata\": {\"session_id\": \"${SESSION_ID}\"},
        \"parts\": [{\"kind\": \"text\", \"text\": \"${PROMPT}\"}]
      }
    }
  }"
```

Parse the JSON response and display the agent's reply clearly, prefixed with the agent name. If the agent is unreachable
or the name is not found in docker-compose.yml, report the error clearly.
