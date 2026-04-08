---
name: remote
description: Send a prompt to a named remote agent via A2A and return the response
argument-hint: "<agent-name> <prompt>"
---

Send a prompt to a named remote agent. The first word of the arguments is the agent name, the rest is the prompt.

**Important:** Always route to the **nyx agent** by name. Requests go to the nyx agent, which routes internally to its configured backend (e.g. `iris-a2-claude`). Never target backend services directly.

**Arguments:** $ARGUMENTS

Use the Bash tool to:

1. Parse the agent name and prompt from the arguments (first word = agent name, remainder = prompt)
2. Search all `docker-compose*.yml` files in the current directory to find the host port mapped to that nyx agent's service (container port 8000)
3. Derive the current session ID from the most recently modified `.jsonl` file in the Claude project directory
4. Send the prompt via A2A JSON-RPC to the nyx agent port

```bash
ARGS="$ARGUMENTS"
TARGET_AGENT=$(echo "$ARGS" | awk '{print $1}')
PROMPT=$(echo "$ARGS" | cut -d' ' -f2-)
PORT=$(grep -l "^  ${TARGET_AGENT}:" docker-compose*.yml 2>/dev/null | xargs -I{} grep -A5 "^  ${TARGET_AGENT}:" {} | grep -o '"[0-9]*:8000"' | cut -d'"' -f2 | cut -d':' -f1)
PROJECT_PATH=$(pwd | sed 's|^/||' | tr '/.' '-')
SESSION_ID=$(ls -t ~/.claude/projects/${PROJECT_PATH}/*.jsonl 2>/dev/null | head -1 | xargs basename -s .jsonl)
MESSAGE_ID=$(python3 -c "import uuid; print(uuid.uuid4())")

curl -s -X POST http://localhost:${PORT}/ \
  -H "Content-Type: application/json" \
  -d "$(jq -n \
    --arg messageId "$MESSAGE_ID" \
    --arg sessionId "$SESSION_ID" \
    --arg text "$PROMPT" \
    '{jsonrpc:"2.0",method:"message/send",id:1,params:{message:{messageId:$messageId,role:"user",metadata:{session_id:$sessionId},parts:[{kind:"text",text:$text}]}}}')"
```

Parse the JSON response and display the agent's reply clearly, prefixed with the agent name. If the agent is unreachable
or the name is not found in any docker-compose*.yml file, report the error clearly.
