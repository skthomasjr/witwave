---
name: message
description: Send a message to another agent on the team via A2A
argument-hint: "<agent-name> <message>"
---

Send a message to a named teammate via A2A. You may only message other agents — not yourself.

**Arguments:** $ARGUMENTS

Use the Bash tool to:

1. Parse the target agent name and message from the arguments (first word = agent name, remainder = message)
2. Read `~/manifest.json` to find the target agent's URL
3. Verify the target agent is not yourself — your name is in the `AGENT_NAME` environment variable. If the target
   matches your name, stop and report that you cannot send a message to yourself.
4. Construct and run the curl command below, inserting your session ID (from your system prompt) as a literal string
   value in the metadata field.

```bash
ARGS="$ARGUMENTS"
TARGET=$(echo "$ARGS" | awk '{print $1}')
MESSAGE=$(echo "$ARGS" | cut -d' ' -f2-)
MY_NAME=$AGENT_NAME

if [ "$TARGET" = "$MY_NAME" ]; then
  echo "Error: Cannot send a message to yourself."
  exit 1
fi

URL=$(cat ~/manifest.json | python3 -c "import sys,json; team=json.load(sys.stdin)['team']; match=[a['url'] for a in team if a['name']=='$TARGET']; print(match[0] if match else '')")

if [ -z "$URL" ]; then
  echo "Error: Agent '$TARGET' not found in manifest."
  exit 1
fi

MESSAGE_ID=$(python3 -c "import uuid; print(uuid.uuid4())")

curl -s -X POST ${URL}/ \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"message/send\",\"id\":1,\"params\":{\"message\":{\"messageId\":\"${MESSAGE_ID}\",\"role\":\"user\",\"metadata\":{\"session_id\":\"<your-session-id>\"},\"parts\":[{\"kind\":\"text\",\"text\":$(echo "$MESSAGE" | python3 -c \"import sys,json; print(json.dumps(sys.stdin.read().strip()))\")}]}}}"
```

Replace `<your-session-id>` with your actual session ID from your system prompt before running. Do not use a variable —
write the literal UUID value directly into the command.

Parse the JSON response and display the agent's reply. If the agent is unreachable or not in the manifest, report the
error clearly.
