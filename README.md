# autonomous-agent

An autonomous agent built on the [Claude Agent SDK](https://platform.claude.com/docs/en/agent-sdk/overview) —
persistent, self-directed, with its own identity, memory, schedule, and the ability to communicate with other agents and
humans.

Each agent runs continuously, acts on its own schedule via heartbeat and agenda, and communicates over the
[A2A protocol](https://a2a-protocol.org). Multiple agents can collaborate as a team, but that's a byproduct — the agent
itself is the unit.

## How It Works

Each agent:

- Runs as a containerized worker with a full professional toolset
- Uses the [Claude Agent SDK](https://platform.claude.com/docs/en/agent-sdk/overview) as its runtime
- Exposes an [A2A (Agent-to-Agent)](https://a2a-protocol.org) interface for communication
- Has its own identity, memory, and configuration
- Acts proactively via a heartbeat and a configurable agenda

## Requirements

- Docker
- Docker Compose
- A Claude Code OAuth token (`claude setup-token`) or Anthropic API key

## Getting Started

### 1. Build the image

```bash
docker build -t claude-agent:latest .
```

### 2. Configure credentials

```bash
export CLAUDE_CODE_OAUTH_TOKEN=your-token-here
```

### 3. Start the agents

```bash
docker compose up -d
```

### 4. Verify

```bash
curl http://localhost:8000/.well-known/agent.json
```

## Agent Structure

Agents are defined under `.agents/`. Each agent has its own identity, configuration, and memory.

```text
.agents/
├── iris/              # Iris (port 8000)
├── nova/              # Nova (port 8001)
└── kira/              # Kira (port 8002)
```

Each agent directory contains:

```text
<agent>/
├── agent.md           # Agent identity — served via A2A agent card
├── logs/              # Conversation log (runtime, not committed)
└── .claude/
    ├── CLAUDE.md      # Behavioral configuration
    ├── HEARTBEAT.md   # Heartbeat schedule and prompt
    ├── agenda/        # Scheduled work items
    └── memory/        # Personal memory (runtime, not committed)
```

## Adding an Agent

1. Copy an existing agent directory:

   ```bash
   cp -r .agents/iris .agents/<name>
   ```

2. Update `.agents/<name>/agent.md` with the agent's identity and role

3. Add the agent to `docker-compose.yml` with the next available port

4. Add the agent to the port table in `CLAUDE.md`

5. Start the agent:

   ```bash
   docker compose up -d <name>
   ```

## Communication

Agents communicate over the [A2A protocol](https://a2a-protocol.org) via JSON-RPC. Each agent exposes:

- `/.well-known/agent.json` — agent card (identity and capabilities)
- `/` — A2A JSON-RPC endpoint (`message/send`)
- `GET /health/start` — startup probe: 200 once ready, 503 while initializing
- `GET /health/live` — liveness probe: always 200 with `{"status": "ok", "agent": ..., "uptime_seconds": ...}`
- `GET /health/ready` — readiness probe: 200/`{"status": "ready"}` or 503/`{"status": "starting"}`

## Memory

Each agent has personal memory at `~/.claude/memory/`. Memory files are markdown documents written and read by the agent
at runtime. They are not committed to source control.

## Authentication

Three authentication methods are supported, configured via environment variable:

| Method             | Environment variable                                         |
| ------------------ | ------------------------------------------------------------ |
| Claude Max (OAuth) | `CLAUDE_CODE_OAUTH_TOKEN`                                    |
| Anthropic API key  | `ANTHROPIC_API_KEY`                                          |
| AWS Bedrock        | `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` + `AWS_REGION` |

## Configuration

| Environment variable           | Default                                             | Description                                                          |
| ------------------------------ | --------------------------------------------------- | -------------------------------------------------------------------- |
| `AGENT_NAME`                   | `claude-agent`                                      | Agent display name                                                   |
| `AGENT_PORT`                   | `8000`                                              | HTTP port the agent listens on                                       |
| `AGENT_VERSION`                | `0.1.0`                                             | Version string reported in the A2A agent card                        |
| `ALLOWED_TOOLS`                | `Read,Write,Edit,Bash,Glob,Grep,WebSearch,WebFetch` | Comma-separated list of Claude Code tools to enable                  |
| `MAX_SESSIONS`                 | `10000`                                             | Maximum number of concurrent sessions tracked in memory              |
| `TASK_TIMEOUT_SECONDS`         | `300`                                               | Seconds before an individual SDK query call is cancelled             |
| `MCP_CONFIG_PATH`              | `/home/agent/.claude/mcp.json`                      | Path to the MCP server configuration file                            |
| `METRICS_ENABLED`              | _(unset)_                                           | Set to any non-empty value to expose Prometheus `/metrics`           |
| `CLAUDE_MODEL`                 | _(unset)_                                           | Override the default Claude model used by the SDK                    |
| `CONTEXT_USAGE_WARN_THRESHOLD` | `0.9`                                               | Fraction of context window (0–1) at which a usage warning is emitted |

## Metrics

When `METRICS_ENABLED` is set, Prometheus metrics are served at `/metrics`.

| Metric                                             | Type      | Labels            | Description                                                           |
| -------------------------------------------------- | --------- | ----------------- | --------------------------------------------------------------------- |
| `agent_a2a_last_request_timestamp_seconds`         | Gauge     | _(none)_          | Unix epoch of the most recent A2A request received                    |
| `agent_a2a_request_duration_seconds`               | Histogram | _(none)_          | Wall-clock duration of each A2A execute() call                        |
| `agent_a2a_requests_total`                         | Counter   | `status`          | Total A2A HTTP requests; `status` is `success` or `error`             |
| `agent_up`                                         | Gauge     | `agent`           | Set to `1` while the agent process is running                         |
| `agent_active_sessions`                            | Gauge     | _(none)_          | Current number of sessions tracked in the LRU cache                   |
| `agent_agenda_checkpoint_stale_total`              | Counter   | _(none)_          | Total stale checkpoint files found during agenda startup scan         |
| `agent_checkpoint_write_errors_total`              | Counter   | _(none)_          | Total agenda checkpoint I/O failures                                  |
| `agent_agenda_duration_seconds`                    | Histogram | `name`            | Wall-clock seconds per agenda item execution                          |
| `agent_agenda_error_duration_seconds`              | Histogram | `name`            | Wall-clock seconds for agenda items that end in error                 |
| `agent_agenda_item_last_error_timestamp_seconds`   | Gauge     | `name`            | Unix epoch of each agenda item's last failed run                      |
| `agent_agenda_item_last_success_timestamp_seconds` | Gauge     | `name`            | Unix epoch of each agenda item's last successful run                  |
| `agent_agenda_lag_seconds`                         | Histogram | _(none)_          | Delay between scheduled and actual agenda item execution start        |
| `agent_agenda_parse_errors_total`                  | Counter   | _(none)_          | Total agenda file parse failures                                      |
| `agent_agenda_items_registered`                    | Gauge     | _(none)_          | Number of currently registered agenda items                           |
| `agent_agenda_reloads_total`                       | Counter   | _(none)_          | Total agenda file-change reload events                                |
| `agent_agenda_running_items`                       | Gauge     | _(none)_          | Number of agenda items currently executing                            |
| `agent_agenda_runs_total`                          | Counter   | `name`, `status`  | Total agenda item executions; `status` is `success` or `error`        |
| `agent_agenda_skips_total`                         | Counter   | `name`            | Total agenda item skips due to previous run still in progress         |
| `agent_bus_consumer_idle_seconds`                  | Histogram | _(none)_          | Idle time between consecutive bus worker processing cycles            |
| `agent_bus_dedup_total`                            | Counter   | `kind`            | Total messages dropped by try_send() due to pending same-kind message |
| `agent_bus_error_processing_duration_seconds`      | Histogram | `kind`            | Wall-clock seconds for bus messages that end in error                 |
| `agent_bus_errors_total`                           | Counter   | _(none)_          | Total unhandled errors in the bus worker                              |
| `agent_bus_last_processed_timestamp_seconds`       | Gauge     | _(none)_          | Unix epoch of the most recent message processed by the bus worker     |
| `agent_bus_messages_total`                         | Counter   | `kind`            | Total messages processed through the message bus                      |
| `agent_bus_processing_duration_seconds`            | Histogram | `kind`            | End-to-end processing time for each bus message                       |
| `agent_bus_pending_kinds`                          | Gauge     | _(none)_          | Number of distinct message kinds currently queued in the bus          |
| `agent_bus_queue_depth`                            | Gauge     | _(none)_          | Current depth of the message bus queue                                |
| `agent_bus_wait_seconds`                           | Histogram | `kind`            | Seconds a message waited in the bus queue before processing           |
| `agent_concurrent_queries`                         | Gauge     | _(none)_          | Number of run() calls currently in flight                             |
| `agent_context_exhaustion_total`                   | Counter   | _(none)_          | Total context window exhaustion events (usage >= 100%)                |
| `agent_context_tokens`                             | Histogram | _(none)_          | Absolute token count from get_context_usage() per SDK turn            |
| `agent_context_usage_percent`                      | Histogram | _(none)_          | Context window utilization percentage per SDK turn                    |
| `agent_context_warnings_total`                     | Counter   | _(none)_          | Total context usage threshold warnings                                |
| `agent_empty_responses_total`                      | Counter   | _(none)_          | Total tasks that produced no text output                              |
| `agent_file_watcher_restarts_total`                | Counter   | `watcher`         | Total file watcher restarts due to missing or deleted directory       |
| `agent_heartbeat_duration_seconds`                 | Histogram | _(none)_          | Wall-clock seconds from heartbeat firing to response received         |
| `agent_heartbeat_error_duration_seconds`           | Histogram | _(none)_          | Wall-clock seconds for heartbeats that end in error                   |
| `agent_heartbeat_lag_seconds`                      | Histogram | _(none)_          | Delay between scheduled and actual heartbeat execution start          |
| `agent_heartbeat_last_error_timestamp_seconds`     | Gauge     | _(none)_          | Unix epoch of the most recent failed heartbeat                        |
| `agent_heartbeat_last_success_timestamp_seconds`   | Gauge     | _(none)_          | Unix epoch of the most recent successful heartbeat                    |
| `agent_heartbeat_load_errors_total`                | Counter   | _(none)_          | Total heartbeat config load/parse failures                            |
| `agent_heartbeat_reloads_total`                    | Counter   | _(none)_          | Total HEARTBEAT.md file-change reload events                          |
| `agent_heartbeat_runs_total`                       | Counter   | `status`          | Total heartbeat executions; `status` is `success` or `error`          |
| `agent_health_checks_total`                        | Counter   | `probe`           | Total HTTP health endpoint hits; `probe` is `start`, `live`, `ready`  |
| `agent_heartbeat_skips_total`                      | Counter   | _(none)_          | Total heartbeat skips due to previous heartbeat still pending         |
| `agent_info`                                       | Info      | `version`,`agent` | Static agent metadata (version and name)                              |
| `agent_log_entries_total`                          | Counter   | `logger`          | Total log entries written; `logger` is `conversation` or `trace`      |
| `agent_log_write_errors_total`                     | Counter   | _(none)_          | Total I/O failures in the conversation/trace logging subsystem        |
| `agent_lru_cache_utilization_percent`              | Gauge     | _(none)_          | LRU session cache utilization as a percentage of MAX_SESSIONS         |
| `agent_mcp_config_errors_total`                    | Counter   | _(none)_          | Total MCP config file parse/load failures                             |
| `agent_mcp_config_reloads_total`                   | Counter   | _(none)_          | Total MCP config file reload events                                   |
| `agent_mcp_servers_active`                         | Gauge     | _(none)_          | Number of currently loaded MCP servers                                |
| `agent_model_requests_total`                       | Counter   | `model`           | Total requests per resolved model                                     |
| `agent_prompt_length_bytes`                        | Histogram | _(none)_          | Byte length of incoming prompts passed to run()                       |
| `agent_response_length_bytes`                      | Histogram | _(none)_          | Byte length of responses returned by run()                            |
| `agent_running_tasks`                              | Gauge     | _(none)_          | Number of currently in-progress tasks                                 |
| `agent_sdk_client_errors_total`                    | Counter   | _(none)_          | Total ClaudeSDKClient connection-level failures (setup/teardown)      |
| `agent_sdk_context_fetch_errors_total`             | Counter   | _(none)_          | Total get_context_usage() call failures                               |
| `agent_sdk_errors_total`                           | Counter   | _(none)_          | Total stderr lines emitted by the Claude SDK subprocess               |
| `agent_sdk_tokens_per_query`                       | Histogram | _(none)_          | Aggregate token count from final get_context_usage() per run_query()  |
| `agent_sdk_tool_calls_per_query`                   | Histogram | _(none)_          | Number of tool calls per run_query() invocation                       |
| `agent_sdk_tool_calls_total`                       | Counter   | `tool`            | Total tool calls by tool name                                         |
| `agent_sdk_tool_duration_seconds`                  | Histogram | `tool`            | Wall-clock seconds per tool call from ToolUseBlock to ToolResultBlock |
| `agent_sdk_tool_errors_total`                      | Counter   | `tool`            | Total tool execution errors by tool name                              |
| `agent_sdk_messages_per_query`                     | Histogram | _(none)_          | Number of SDK messages received per run_query() call                  |
| `agent_sdk_query_duration_seconds`                 | Histogram | _(none)_          | Raw SDK query time in seconds inside run_query()                      |
| `agent_sdk_query_error_duration_seconds`           | Histogram | _(none)_          | Wall-clock seconds for run_query() calls that end in error            |
| `agent_sdk_result_errors_total`                    | Counter   | _(none)_          | Total SDK ResultMessage errors returned during run_query()            |
| `agent_sdk_session_duration_seconds`               | Histogram | _(none)_          | Raw SDK connection lifetime in seconds (ClaudeSDKClient block)        |
| `agent_sdk_subprocess_spawn_duration_seconds`      | Histogram | _(none)_          | Time to initialize the ClaudeSDKClient subprocess                     |
| `agent_startup_duration_seconds`                   | Gauge     | _(none)_          | Time from process start to ready state in seconds                     |
| `agent_stderr_lines_per_task`                      | Histogram | _(none)_          | Number of SDK stderr lines captured per run() invocation              |
| `agent_session_age_seconds`                        | Histogram | _(none)_          | Age of a session in seconds when evicted from the LRU cache           |
| `agent_session_idle_seconds`                       | Histogram | _(none)_          | Seconds a session was idle before being resumed                       |
| `agent_session_evictions_total`                    | Counter   | _(none)_          | Total session evictions due to LRU cap                                |
| `agent_session_starts_total`                       | Counter   | `type`            | Total session starts; `type` is `new` or `resumed`                    |
| `agent_task_cancellations_total`                   | Counter   | _(none)_          | Total task cancellation requests                                      |
| `agent_task_duration_seconds`                      | Histogram | _(none)_          | Wall-clock seconds for successful task executions                     |
| `agent_task_error_duration_seconds`                | Histogram | _(none)_          | Wall-clock seconds for tasks that end in error or timeout             |
| `agent_task_last_error_timestamp_seconds`          | Gauge     | _(none)_          | Unix epoch of the most recent failed task execution                   |
| `agent_task_last_success_timestamp_seconds`        | Gauge     | _(none)_          | Unix epoch of the most recent successful task execution               |
| `agent_task_timeout_headroom_seconds`              | Histogram | _(none)_          | Remaining timeout budget when a task completes successfully           |
| `agent_task_retries_total`                         | Counter   | _(none)_          | Total task retries due to session already in use                      |
| `agent_tasks_total`                                | Counter   | `status`          | Total tasks processed; `status` is `success`, `error`, or `timeout`   |
| `agent_tasks_with_stderr_total`                    | Counter   | _(none)_          | Total task executions that produced any SDK stderr output             |
| `agent_text_blocks_per_query`                      | Histogram | _(none)_          | Number of text blocks returned per run_query() invocation             |
| `agent_uptime_seconds`                             | Gauge     | _(none)_          | Agent uptime in seconds, computed on each Prometheus scrape           |
| `agent_watcher_events_total`                       | Counter   | `watcher`         | Total raw file-system change events detected by each watcher          |
