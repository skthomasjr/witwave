"""Prometheus metrics for the codex backend agent."""

import os

import prometheus_client

_enabled = bool(os.environ.get("METRICS_ENABLED"))

# Service-level metrics
backend_up: prometheus_client.Gauge | None = None
backend_info: prometheus_client.Info | None = None
backend_uptime_seconds: prometheus_client.Gauge | None = None
backend_startup_duration_seconds: prometheus_client.Gauge | None = None
backend_event_loop_lag_seconds: prometheus_client.Histogram | None = None
backend_health_checks_total: prometheus_client.Counter | None = None
backend_task_restarts_total: prometheus_client.Counter | None = None

# A2A request metrics
backend_a2a_requests_total: prometheus_client.Counter | None = None
backend_a2a_request_duration_seconds: prometheus_client.Histogram | None = None
backend_a2a_last_request_timestamp_seconds: prometheus_client.Gauge | None = None

# Task execution metrics
backend_tasks_total: prometheus_client.Counter | None = None
backend_task_duration_seconds: prometheus_client.Histogram | None = None
backend_task_error_duration_seconds: prometheus_client.Histogram | None = None
backend_task_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
backend_task_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
backend_task_timeout_headroom_seconds: prometheus_client.Histogram | None = None
backend_task_cancellations_total: prometheus_client.Counter | None = None
backend_running_tasks: prometheus_client.Gauge | None = None
backend_concurrent_queries: prometheus_client.Gauge | None = None

# Session metrics
backend_active_sessions: prometheus_client.Gauge | None = None
backend_session_starts_total: prometheus_client.Counter | None = None
backend_session_evictions_total: prometheus_client.Counter | None = None
backend_session_age_seconds: prometheus_client.Histogram | None = None
backend_session_idle_seconds: prometheus_client.Histogram | None = None
backend_lru_cache_utilization_percent: prometheus_client.Gauge | None = None
backend_session_history_save_errors_total: prometheus_client.Counter | None = None

# Prompt / response size metrics
backend_prompt_length_bytes: prometheus_client.Histogram | None = None
backend_response_length_bytes: prometheus_client.Histogram | None = None
backend_empty_responses_total: prometheus_client.Counter | None = None
backend_empty_prompts_total: prometheus_client.Counter | None = None

# Model / backend routing metrics
backend_model_requests_total: prometheus_client.Counter | None = None

# Logging subsystem metrics
backend_log_bytes_total: prometheus_client.Counter | None = None
backend_log_entries_total: prometheus_client.Counter | None = None
backend_log_write_errors_total: prometheus_client.Counter | None = None

# SDK metrics
backend_sdk_query_duration_seconds: prometheus_client.Histogram | None = None
backend_sdk_query_error_duration_seconds: prometheus_client.Histogram | None = None
backend_sdk_time_to_first_message_seconds: prometheus_client.Histogram | None = None
backend_sdk_session_duration_seconds: prometheus_client.Histogram | None = None
backend_sdk_messages_per_query: prometheus_client.Histogram | None = None
backend_sdk_turns_per_query: prometheus_client.Histogram | None = None
backend_text_blocks_per_query: prometheus_client.Histogram | None = None
backend_sdk_tokens_per_query: prometheus_client.Histogram | None = None
backend_streaming_events_emitted_total: prometheus_client.Counter | None = None
backend_streaming_chunks_dropped_total: prometheus_client.Counter | None = None

# MCP config metrics (parity with claude — #432)
backend_mcp_config_errors_total: prometheus_client.Counter | None = None
backend_mcp_config_reloads_total: prometheus_client.Counter | None = None
backend_mcp_servers_active: prometheus_client.Gauge | None = None
backend_mcp_command_rejected_total: prometheus_client.Counter | None = None

# SDK error classification metrics (parity with claude — #431)
backend_sdk_errors_total: prometheus_client.Counter | None = None
backend_sdk_result_errors_total: prometheus_client.Counter | None = None
backend_sdk_client_errors_total: prometheus_client.Counter | None = None

# File watcher metrics
backend_watcher_events_total: prometheus_client.Counter | None = None
backend_file_watcher_restarts_total: prometheus_client.Counter | None = None

# Context window metrics
backend_context_tokens: prometheus_client.Histogram | None = None
backend_context_tokens_remaining: prometheus_client.Histogram | None = None
backend_context_usage_percent: prometheus_client.Histogram | None = None
backend_context_exhaustion_total: prometheus_client.Counter | None = None
backend_context_warnings_total: prometheus_client.Counter | None = None

# Tool-call metrics
backend_sdk_tool_calls_total: prometheus_client.Counter | None = None
backend_sdk_tool_calls_per_query: prometheus_client.Histogram | None = None
backend_sdk_tool_duration_seconds: prometheus_client.Histogram | None = None
backend_sdk_tool_errors_total: prometheus_client.Counter | None = None
backend_sdk_tool_call_input_size_bytes: prometheus_client.Histogram | None = None
backend_sdk_tool_result_size_bytes: prometheus_client.Histogram | None = None

# Token budget metrics
backend_budget_exceeded_total: prometheus_client.Counter | None = None

# Hooks / tool-audit (#586) — shell-only baseline scope.
# Non-shell enforcement and the rest of the backend_hooks_* family stay deferred
# until a tool-wrapping proxy design is validated against the Agents SDK.
backend_codex_hooks_denials_total: prometheus_client.Counter | None = None
backend_tool_audit_entries_total: prometheus_client.Counter | None = None

if _enabled:
    backend_up = prometheus_client.Gauge("backend_up", "Backend agent is running", ["agent", "agent_id", "backend"])
    backend_info = prometheus_client.Info("a2", "Static backend agent metadata.")
    backend_uptime_seconds = prometheus_client.Gauge(
        "backend_uptime_seconds",
        "Backend agent uptime in seconds, computed on each Prometheus scrape.",
        ["agent", "agent_id", "backend"],
    )
    backend_startup_duration_seconds = prometheus_client.Gauge(
        "backend_startup_duration_seconds",
        "Time from process start to ready state in seconds.",
        ["agent", "agent_id", "backend"],
    )
    backend_event_loop_lag_seconds = prometheus_client.Histogram(
        "backend_event_loop_lag_seconds",
        "Excess delay beyond expected sleep duration, measuring asyncio event loop congestion.",
        ["agent", "agent_id", "backend"],
        buckets=(0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0),
    )
    backend_health_checks_total = prometheus_client.Counter(
        "backend_health_checks_total",
        "Total HTTP health endpoint hits by probe type.",
        ["agent", "agent_id", "backend", "probe"],
    )
    backend_task_restarts_total = prometheus_client.Counter(
        "backend_task_restarts_total",
        "Total worker restarts by the _guarded() loop after an unexpected exception.",
        ["agent", "agent_id", "backend", "task"],
    )

    # A2A
    backend_a2a_requests_total = prometheus_client.Counter(
        "backend_a2a_requests_total",
        "Total A2A HTTP requests by outcome.",
        ["agent", "agent_id", "backend", "status"],
    )
    backend_a2a_request_duration_seconds = prometheus_client.Histogram(
        "backend_a2a_request_duration_seconds",
        "Wall-clock duration of each A2A execute() call.",
        ["agent", "agent_id", "backend"],
        buckets=(0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600),
    )
    backend_a2a_last_request_timestamp_seconds = prometheus_client.Gauge(
        "backend_a2a_last_request_timestamp_seconds",
        "Unix epoch of the most recent A2A request received.",
        ["agent", "agent_id", "backend"],
    )

    # Tasks
    backend_tasks_total = prometheus_client.Counter(
        "backend_tasks_total",
        "Total agent tasks processed by outcome.",
        ["agent", "agent_id", "backend", "status"],
    )
    backend_task_duration_seconds = prometheus_client.Histogram(
        "backend_task_duration_seconds",
        "Duration of agent tasks in seconds.",
        ["agent", "agent_id", "backend"],
        buckets=(0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600),
    )
    backend_task_error_duration_seconds = prometheus_client.Histogram(
        "backend_task_error_duration_seconds",
        "Wall-clock seconds for tasks that end in error or timeout.",
        ["agent", "agent_id", "backend"],
        buckets=(0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600),
    )
    backend_task_last_success_timestamp_seconds = prometheus_client.Gauge(
        "backend_task_last_success_timestamp_seconds",
        "Unix epoch of the most recent successful task execution.",
        ["agent", "agent_id", "backend"],
    )
    backend_task_last_error_timestamp_seconds = prometheus_client.Gauge(
        "backend_task_last_error_timestamp_seconds",
        "Unix epoch of the most recent failed task execution.",
        ["agent", "agent_id", "backend"],
    )
    backend_task_timeout_headroom_seconds = prometheus_client.Histogram(
        "backend_task_timeout_headroom_seconds",
        "Remaining timeout budget when a task completes successfully.",
        ["agent", "agent_id", "backend"],
        buckets=(0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600),
    )
    backend_task_cancellations_total = prometheus_client.Counter(
        "backend_task_cancellations_total",
        "Total task cancellation requests.",
        ["agent", "agent_id", "backend"],
    )
    backend_running_tasks = prometheus_client.Gauge(
        "backend_running_tasks",
        "Number of currently in-progress tasks.",
        ["agent", "agent_id", "backend"],
    )
    backend_concurrent_queries = prometheus_client.Gauge(
        "backend_concurrent_queries",
        "Number of run() calls currently in flight.",
        ["agent", "agent_id", "backend"],
    )

    # Sessions
    backend_active_sessions = prometheus_client.Gauge(
        "backend_active_sessions",
        "Number of active sessions tracked in the LRU cache.",
        ["agent", "agent_id", "backend"],
    )
    backend_session_starts_total = prometheus_client.Counter(
        "backend_session_starts_total",
        "Total session starts by type.",
        ["agent", "agent_id", "backend", "type"],
    )
    backend_session_evictions_total = prometheus_client.Counter(
        "backend_session_evictions_total",
        "Total session evictions due to LRU cap.",
        ["agent", "agent_id", "backend"],
    )
    backend_session_age_seconds = prometheus_client.Histogram(
        "backend_session_age_seconds",
        "Seconds since last use when a session is evicted from the LRU cache.",
        ["agent", "agent_id", "backend"],
        buckets=(60, 300, 900, 1800, 3600, 7200, 14400, 28800, 86400),
    )
    backend_session_idle_seconds = prometheus_client.Histogram(
        "backend_session_idle_seconds",
        "Seconds a session was idle before being resumed.",
        ["agent", "agent_id", "backend"],
        buckets=(60, 300, 900, 1800, 3600, 7200, 14400, 28800, 86400),
    )
    backend_lru_cache_utilization_percent = prometheus_client.Gauge(
        "backend_lru_cache_utilization_percent",
        "LRU session cache utilization as a percentage of MAX_SESSIONS.",
        ["agent", "agent_id", "backend"],
    )
    backend_session_history_save_errors_total = prometheus_client.Counter(
        "backend_session_history_save_errors_total",
        "Total failures to initialise or write the session SQLite store.",
        ["agent", "agent_id", "backend"],
    )

    # Prompt / response
    backend_prompt_length_bytes = prometheus_client.Histogram(
        "backend_prompt_length_bytes",
        "Byte length of incoming prompts passed to run().",
        ["agent", "agent_id", "backend"],
        buckets=(100, 500, 1_000, 5_000, 10_000, 50_000, 100_000, 500_000, 1_000_000, 5_000_000),
    )
    backend_response_length_bytes = prometheus_client.Histogram(
        "backend_response_length_bytes",
        "Byte length of responses returned by run().",
        ["agent", "agent_id", "backend"],
        buckets=(100, 500, 1_000, 5_000, 10_000, 50_000, 100_000, 500_000, 1_000_000, 5_000_000),
    )
    backend_empty_responses_total = prometheus_client.Counter(
        "backend_empty_responses_total",
        "Total tasks that produced no text output.",
        ["agent", "agent_id", "backend"],
    )
    backend_empty_prompts_total = prometheus_client.Counter(
        "backend_empty_prompts_total",
        "Total execute() invocations rejected because the resolved prompt was empty or whitespace-only (#544 / #801).",
        ["agent", "agent_id", "backend"],
    )

    # Model routing
    backend_model_requests_total = prometheus_client.Counter(
        "backend_model_requests_total",
        "Total requests per resolved model.",
        ["agent", "agent_id", "backend", "model"],
    )

    # Logging
    backend_log_bytes_total = prometheus_client.Counter(
        "backend_log_bytes_total",
        "Total bytes written by the logging subsystem.",
        ["agent", "agent_id", "backend", "logger"],
    )
    backend_log_entries_total = prometheus_client.Counter(
        "backend_log_entries_total",
        "Total log entries written by logger type.",
        ["agent", "agent_id", "backend", "logger"],
    )
    backend_log_write_errors_total = prometheus_client.Counter(
        "backend_log_write_errors_total",
        "Total I/O failures in the conversation/trace logging subsystem.",
        ["agent", "agent_id", "backend"],
    )

    # SDK
    backend_sdk_query_duration_seconds = prometheus_client.Histogram(
        "backend_sdk_query_duration_seconds",
        "Raw backend query time in seconds inside run_query().",
        ["agent", "agent_id", "backend", "model"],
        buckets=(0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60),
    )
    backend_sdk_query_error_duration_seconds = prometheus_client.Histogram(
        "backend_sdk_query_error_duration_seconds",
        "Wall-clock seconds for run_query() calls that end in error.",
        ["agent", "agent_id", "backend", "model"],
        buckets=(0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60),
    )
    backend_sdk_time_to_first_message_seconds = prometheus_client.Histogram(
        "backend_sdk_time_to_first_message_seconds",
        "Seconds from query submission to the first response message.",
        ["agent", "agent_id", "backend", "model"],
        buckets=(0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60),
    )
    backend_sdk_session_duration_seconds = prometheus_client.Histogram(
        "backend_sdk_session_duration_seconds",
        "Backend session/connection lifetime in seconds.",
        ["agent", "agent_id", "backend", "model"],
        buckets=(0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600),
    )
    backend_sdk_messages_per_query = prometheus_client.Histogram(
        "backend_sdk_messages_per_query",
        "Number of backend messages received per run_query() call.",
        ["agent", "agent_id", "backend", "model"],
        buckets=(1, 2, 5, 10, 20, 50, 100, 200),
    )
    backend_sdk_turns_per_query = prometheus_client.Histogram(
        "backend_sdk_turns_per_query",
        "Number of assistant turns per run_query() invocation.",
        ["agent", "agent_id", "backend", "model"],
        buckets=(1, 2, 3, 5, 10, 20, 50, 100),
    )
    backend_text_blocks_per_query = prometheus_client.Histogram(
        "backend_text_blocks_per_query",
        "Number of text blocks returned per run_query() invocation.",
        ["agent", "agent_id", "backend", "model"],
        buckets=(0, 1, 2, 5, 10, 20, 50, 100),
    )
    backend_sdk_tokens_per_query = prometheus_client.Histogram(
        "backend_sdk_tokens_per_query",
        "Total tokens consumed per run_query() invocation (parity with claude — #459).",
        ["agent", "agent_id", "backend", "model"],
        buckets=(100, 500, 1_000, 5_000, 10_000, 25_000, 50_000, 100_000, 200_000, 500_000),
    )
    backend_streaming_events_emitted_total = prometheus_client.Counter(
        "backend_streaming_events_emitted_total",
        "Total partial agent_text_message events enqueued during streaming. "
        "Equals the number of text deltas the executor pushed to the A2A "
        "event_queue mid-stream (#430).",
        ["agent", "agent_id", "backend", "model"],
    )
    # Streaming chunks dropped due to the on_chunk consumer exceeding
    # STREAM_CHUNK_TIMEOUT_SECONDS (#724). Label schema mirrors
    # streaming_events_emitted so dashboards can union the two series.
    backend_streaming_chunks_dropped_total = prometheus_client.Counter(
        "backend_streaming_chunks_dropped_total",
        "Total streaming chunks dropped because the A2A consumer's on_chunk "
        "callback exceeded STREAM_CHUNK_TIMEOUT_SECONDS. The final-flush "
        "aggregated text still fires at response completion so clients see "
        "the complete output (#724).",
        ["agent", "agent_id", "backend", "model"],
    )

    # MCP config (parity with claude — #432)
    backend_mcp_config_errors_total = prometheus_client.Counter(
        "backend_mcp_config_errors_total",
        "Total errors loading the MCP config file (mcp.json). Counts both "
        "missing-file silently-ignored cases (no increment) and parse / "
        "I/O failures (incremented).",
        ["agent", "agent_id", "backend"],
    )
    backend_mcp_config_reloads_total = prometheus_client.Counter(
        "backend_mcp_config_reloads_total",
        "Total successful reloads of mcp.json triggered by the file watcher.",
        ["agent", "agent_id", "backend"],
    )
    backend_mcp_servers_active = prometheus_client.Gauge(
        "backend_mcp_servers_active",
        "Number of MCP servers currently loaded from mcp.json (gauge).",
        ["agent", "agent_id", "backend"],
    )
    # Command allow-list rejections (#720 — parity with claude #711).
    backend_mcp_command_rejected_total = prometheus_client.Counter(
        "backend_mcp_command_rejected_total",
        "Total MCP server entries rejected by the command allow-list, by reason.",
        ["agent", "agent_id", "backend", "reason"],
    )

    # SDK error classification (parity with claude — #431)
    backend_sdk_errors_total = prometheus_client.Counter(
        "backend_sdk_errors_total",
        "Total stderr/error lines emitted by the backend subprocess.",
        ["agent", "agent_id", "backend", "model"],
    )
    backend_sdk_result_errors_total = prometheus_client.Counter(
        "backend_sdk_result_errors_total",
        "Total backend result errors returned during run_query().",
        ["agent", "agent_id", "backend", "model"],
    )
    backend_sdk_client_errors_total = prometheus_client.Counter(
        "backend_sdk_client_errors_total",
        "Total backend client connection-level failures (setup/teardown).",
        ["agent", "agent_id", "backend", "model"],
    )

    # File watchers
    backend_watcher_events_total = prometheus_client.Counter(
        "backend_watcher_events_total",
        "Total file watcher change events observed by backend watchers.",
        ["agent", "agent_id", "backend", "watcher"],
    )
    backend_file_watcher_restarts_total = prometheus_client.Counter(
        "backend_file_watcher_restarts_total",
        "Total file watcher restarts after watcher exits unexpectedly.",
        ["agent", "agent_id", "backend", "watcher"],
    )

    # Context window
    backend_context_tokens = prometheus_client.Histogram(
        "backend_context_tokens",
        "Token count used per query (from SDK usage response).",
        ["agent", "agent_id", "backend"],
        buckets=(100, 500, 1_000, 5_000, 10_000, 25_000, 50_000, 100_000, 200_000, 500_000),
    )
    backend_context_tokens_remaining = prometheus_client.Histogram(
        "backend_context_tokens_remaining",
        "Remaining token budget (max_tokens - used) per query.",
        ["agent", "agent_id", "backend"],
        buckets=(1000, 5000, 10000, 25000, 50000, 100000, 150000),
    )
    backend_context_usage_percent = prometheus_client.Histogram(
        "backend_context_usage_percent",
        "Context window utilization percentage per query.",
        ["agent", "agent_id", "backend"],
        buckets=(50, 70, 80, 90, 95, 99, 100),
    )
    backend_context_exhaustion_total = prometheus_client.Counter(
        "backend_context_exhaustion_total",
        "Total context window exhaustion events (usage >= 100%).",
        ["agent", "agent_id", "backend"],
    )
    backend_context_warnings_total = prometheus_client.Counter(
        "backend_context_warnings_total",
        "Total context usage threshold warnings (usage >= 80%).",
        ["agent", "agent_id", "backend"],
    )

    # Tool calls
    backend_sdk_tool_calls_total = prometheus_client.Counter(
        "backend_sdk_tool_calls_total",
        "Total tool calls by tool name.",
        ["agent", "agent_id", "backend", "tool"],
    )
    backend_sdk_tool_calls_per_query = prometheus_client.Histogram(
        "backend_sdk_tool_calls_per_query",
        "Number of tool calls per run_query() invocation.",
        ["agent", "agent_id", "backend"],
        buckets=(0, 1, 2, 5, 10, 20, 50),
    )
    backend_sdk_tool_duration_seconds = prometheus_client.Histogram(
        "backend_sdk_tool_duration_seconds",
        "Duration of individual tool calls in seconds.",
        ["agent", "agent_id", "backend", "tool"],
        buckets=(0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60),
    )
    backend_sdk_tool_errors_total = prometheus_client.Counter(
        "backend_sdk_tool_errors_total",
        "Total tool call errors by tool name.",
        ["agent", "agent_id", "backend", "tool"],
    )
    backend_sdk_tool_call_input_size_bytes = prometheus_client.Histogram(
        "backend_sdk_tool_call_input_size_bytes",
        "Byte size of tool call input arguments.",
        ["agent", "agent_id", "backend", "tool"],
        buckets=(100, 500, 1_000, 5_000, 10_000, 50_000, 100_000, 500_000, 1_000_000, 5_000_000),
    )
    backend_sdk_tool_result_size_bytes = prometheus_client.Histogram(
        "backend_sdk_tool_result_size_bytes",
        "Byte size of tool call result output.",
        ["agent", "agent_id", "backend", "tool"],
        buckets=(100, 500, 1_000, 5_000, 10_000, 50_000, 100_000, 500_000, 1_000_000, 5_000_000),
    )

    # Token budget
    backend_budget_exceeded_total = prometheus_client.Counter(
        "backend_budget_exceeded_total",
        "Total token budget exceeded events (max_tokens limit hit during execution).",
        ["agent", "agent_id", "backend"],
    )

    # Hooks / tool-audit (#586) — shell-only PreToolUse deny baseline.
    # Narrowly scoped: the `rule` label enumerates the shell baseline rule
    # names that mirror claude's baseline (baseline-rm-rf-root,
    # baseline-git-force-push-main, baseline-curl-pipe-shell,
    # baseline-chmod-777, baseline-dd-device). Non-shell enforcement is not
    # covered by this counter yet — see #586 for the deferred design.
    backend_codex_hooks_denials_total = prometheus_client.Counter(
        "backend_codex_hooks_denials_total",
        "Total shell commands denied by the codex PreToolUse baseline, by rule.",
        ["agent", "agent_id", "backend", "rule"],
    )
    backend_tool_audit_entries_total = prometheus_client.Counter(
        "backend_tool_audit_entries_total",
        "Total rows written to tool-audit.jsonl by codex PostToolUse audit.",
        ["agent", "agent_id", "backend", "tool"],
    )
