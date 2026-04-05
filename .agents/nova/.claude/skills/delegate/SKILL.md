---
name: delegate
description: Delegate a task to a named teammate via A2A and surface the result
argument-hint: "<agent-name> <task>"
---

Delegate a task to a named teammate via A2A and return their response. You may only delegate to other agents — not
yourself.

**Arguments:** $ARGUMENTS

Use the Bash tool to:

1. Parse the target agent name and task from the arguments (first word = agent name, remainder = task description).
2. Read `~/manifest.json` to find the target agent's URL.
3. Verify the target is not yourself — your name is in `AGENT_NAME`. If the target matches your name, stop and report
   the error.
4. Construct and run the curl command below, inserting your session ID (from your system prompt) as a literal string in
   the metadata field.

```bash
ARGS="$ARGUMENTS"
TARGET=$(echo "$ARGS" | awk '{print $1}')
TASK=$(echo "$ARGS" | cut -d' ' -f2-)
MY_NAME=$AGENT_NAME

if [ "$TARGET" = "$MY_NAME" ]; then
  echo "Error: Cannot delegate to yourself."
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
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"message/send\",\"id\":1,\"params\":{\"message\":{\"messageId\":\"${MESSAGE_ID}\",\"role\":\"user\",\"metadata\":{\"session_id\":\"<your-session-id>\"},\"parts\":[{\"kind\":\"text\",\"text\":$(echo "$TASK" | python3 -c \"import sys,json; print(json.dumps(sys.stdin.read().strip()))\")}]}}}"
```

Replace `<your-session-id>` with your actual session ID from your system prompt before running. Do not use a variable —
write the literal UUID value directly.

Parse the JSON response and extract the agent's reply from `result.content[0].parts[0].text`. Surface this as the
delegation result. If the agent is unreachable, not in the manifest, or the response cannot be parsed, report the error
clearly.

## Example

To ask nova to summarise the latest entries in a log file:

```bash
/delegate nova Summarise the last 20 lines of ~/logs/conversation.log and tell me the most recent topic discussed.
```

Nova's reply will be extracted and returned as the result of the delegation.
