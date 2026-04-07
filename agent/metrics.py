"""Prometheus metrics for the autonomous agent."""

import os

import prometheus_client

_enabled = bool(os.environ.get("METRICS_ENABLED"))

agent_a2a_last_request_timestamp_seconds: prometheus_client.Gauge | None = None
agent_a2a_request_duration_seconds: prometheus_client.Histogram | None = None
agent_a2a_requests_total: prometheus_client.Counter | None = None
agent_up: prometheus_client.Gauge | None = None
agent_active_sessions: prometheus_client.Gauge | None = None
agent_agenda_checkpoint_stale_total: prometheus_client.Counter | None = None
agent_checkpoint_write_errors_total: prometheus_client.Counter | None = None
agent_agenda_duration_seconds: prometheus_client.Histogram | None = None
agent_agenda_error_duration_seconds: prometheus_client.Histogram | None = None
agent_agenda_lag_seconds: prometheus_client.Histogram | None = None
agent_agenda_parse_errors_total: prometheus_client.Counter | None = None
agent_agenda_item_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
agent_agenda_item_last_run_timestamp_seconds: prometheus_client.Gauge | None = None
agent_agenda_item_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
agent_agenda_items_registered: prometheus_client.Gauge | None = None
agent_agenda_reloads_total: prometheus_client.Counter | None = None
agent_agenda_running_items: prometheus_client.Gauge | None = None
agent_agenda_runs_total: prometheus_client.Counter | None = None
agent_agenda_skips_total: prometheus_client.Counter | None = None
agent_bus_consumer_idle_seconds: prometheus_client.Histogram | None = None
agent_bus_dedup_total: prometheus_client.Counter | None = None
agent_bus_error_processing_duration_seconds: prometheus_client.Histogram | None = None
agent_bus_errors_total: prometheus_client.Counter | None = None
agent_bus_last_processed_timestamp_seconds: prometheus_client.Gauge | None = None
agent_bus_processing_duration_seconds: prometheus_client.Histogram | None = None
agent_info: prometheus_client.Info | None = None
agent_bus_messages_total: prometheus_client.Counter | None = None
agent_bus_pending_kinds: prometheus_client.Gauge | None = None
agent_bus_queue_depth: prometheus_client.Gauge | None = None
agent_bus_wait_seconds: prometheus_client.Histogram | None = None
agent_concurrent_queries: prometheus_client.Gauge | None = None
agent_context_exhaustion_total: prometheus_client.Counter | None = None
agent_context_tokens: prometheus_client.Histogram | None = None
agent_context_tokens_remaining: prometheus_client.Histogram | None = None
agent_context_usage_percent: prometheus_client.Histogram | None = None
agent_context_warnings_total: prometheus_client.Counter | None = None
agent_empty_responses_total: prometheus_client.Counter | None = None
agent_event_loop_lag_seconds: prometheus_client.Histogram | None = None
agent_file_watcher_restarts_total: prometheus_client.Counter | None = None
agent_heartbeat_duration_seconds: prometheus_client.Histogram | None = None
agent_heartbeat_error_duration_seconds: prometheus_client.Histogram | None = None
agent_heartbeat_lag_seconds: prometheus_client.Histogram | None = None
agent_heartbeat_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
agent_heartbeat_last_run_timestamp_seconds: prometheus_client.Gauge | None = None
agent_heartbeat_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
agent_heartbeat_load_errors_total: prometheus_client.Counter | None = None
agent_heartbeat_runs_total: prometheus_client.Counter | None = None
agent_heartbeat_reloads_total: prometheus_client.Counter | None = None
agent_health_checks_total: prometheus_client.Counter | None = None
agent_heartbeat_skips_total: prometheus_client.Counter | None = None
agent_log_bytes_total: prometheus_client.Counter | None = None
agent_log_entries_total: prometheus_client.Counter | None = None
agent_log_write_errors_total: prometheus_client.Counter | None = None
agent_lru_cache_utilization_percent: prometheus_client.Gauge | None = None
agent_mcp_config_errors_total: prometheus_client.Counter | None = None
agent_mcp_config_reloads_total: prometheus_client.Counter | None = None
agent_mcp_servers_active: prometheus_client.Gauge | None = None
agent_model_requests_total: prometheus_client.Counter | None = None
agent_prompt_length_bytes: prometheus_client.Histogram | None = None
agent_running_tasks: prometheus_client.Gauge | None = None
agent_response_length_bytes: prometheus_client.Histogram | None = None
agent_sdk_client_errors_total: prometheus_client.Counter | None = None
agent_sdk_context_fetch_errors_total: prometheus_client.Counter | None = None
agent_sdk_errors_total: prometheus_client.Counter | None = None
agent_sdk_tokens_per_query: prometheus_client.Histogram | None = None
agent_sdk_tool_call_input_size_bytes: prometheus_client.Histogram | None = None
agent_sdk_tool_calls_per_query: prometheus_client.Histogram | None = None
agent_sdk_tool_duration_seconds: prometheus_client.Histogram | None = None
agent_sdk_turns_per_query: prometheus_client.Histogram | None = None
agent_sdk_tool_calls_total: prometheus_client.Counter | None = None
agent_sdk_tool_errors_total: prometheus_client.Counter | None = None
agent_sdk_tool_result_size_bytes: prometheus_client.Histogram | None = None
agent_sdk_messages_per_query: prometheus_client.Histogram | None = None
agent_sdk_result_errors_total: prometheus_client.Counter | None = None
agent_sdk_session_duration_seconds: prometheus_client.Histogram | None = None
agent_sdk_subprocess_spawn_duration_seconds: prometheus_client.Histogram | None = None
agent_sdk_query_duration_seconds: prometheus_client.Histogram | None = None
agent_sdk_query_error_duration_seconds: prometheus_client.Histogram | None = None
agent_sdk_time_to_first_message_seconds: prometheus_client.Histogram | None = None
agent_startup_duration_seconds: prometheus_client.Gauge | None = None
agent_stderr_lines_per_task: prometheus_client.Histogram | None = None
agent_session_age_seconds: prometheus_client.Histogram | None = None
agent_session_idle_seconds: prometheus_client.Histogram | None = None
agent_session_evictions_total: prometheus_client.Counter | None = None
agent_session_starts_total: prometheus_client.Counter | None = None
agent_task_cancellations_total: prometheus_client.Counter | None = None
agent_task_duration_seconds: prometheus_client.Histogram | None = None
agent_task_error_duration_seconds: prometheus_client.Histogram | None = None
agent_task_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
agent_task_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
agent_task_timeout_headroom_seconds: prometheus_client.Histogram | None = None
agent_uptime_seconds: prometheus_client.Gauge | None = None
agent_task_restarts_total: prometheus_client.Counter | None = None
agent_task_retries_total: prometheus_client.Counter | None = None
agent_tasks_total: prometheus_client.Counter | None = None
agent_tasks_with_stderr_total: prometheus_client.Counter | None = None
agent_text_blocks_per_query: prometheus_client.Histogram | None = None
agent_watcher_events_total: prometheus_client.Counter | None = None

if _enabled:
    agent_a2a_last_request_timestamp_seconds = prometheus_client.Gauge(
        "agent_a2a_last_request_timestamp_seconds",
        "Unix epoch of the most recent A2A request received.",
    )
    agent_a2a_request_duration_seconds = prometheus_client.Histogram(
        "agent_a2a_request_duration_seconds",
        "Wall-clock duration of each A2A execute() call.",
    )
    agent_a2a_requests_total = prometheus_client.Counter(
        "agent_a2a_requests_total",
        "Total A2A HTTP requests by outcome.",
        ["status"],
    )
    agent_up = prometheus_client.Gauge("agent_up", "Agent is running", ["agent"])
    agent_active_sessions = prometheus_client.Gauge(
        "agent_active_sessions",
        "Number of active sessions tracked in the LRU cache.",
    )
    agent_agenda_checkpoint_stale_total = prometheus_client.Counter(
        "agent_agenda_checkpoint_stale_total",
        "Total stale checkpoint files found during agenda startup scan.",
    )
    agent_checkpoint_write_errors_total = prometheus_client.Counter(
        "agent_checkpoint_write_errors_total",
        "Total agenda checkpoint I/O failures.",
    )
    agent_agenda_duration_seconds = prometheus_client.Histogram(
        "agent_agenda_duration_seconds",
        "Wall-clock seconds per agenda item execution.",
        ["name"],
    )
    agent_agenda_error_duration_seconds = prometheus_client.Histogram(
        "agent_agenda_error_duration_seconds",
        "Wall-clock seconds for agenda items that end in error.",
        ["name"],
    )
    agent_agenda_lag_seconds = prometheus_client.Histogram(
        "agent_agenda_lag_seconds",
        "Delay between scheduled and actual agenda item execution start.",
    )
    agent_agenda_parse_errors_total = prometheus_client.Counter(
        "agent_agenda_parse_errors_total",
        "Total agenda file parse failures.",
    )
    agent_agenda_item_last_error_timestamp_seconds = prometheus_client.Gauge(
        "agent_agenda_item_last_error_timestamp_seconds",
        "Unix epoch of each agenda item's last failed run.",
        ["name"],
    )
    agent_agenda_item_last_run_timestamp_seconds = prometheus_client.Gauge(
        "agent_agenda_item_last_run_timestamp_seconds",
        "Unix epoch of each agenda item's most recent execution, regardless of outcome.",
        ["name"],
    )
    agent_agenda_item_last_success_timestamp_seconds = prometheus_client.Gauge(
        "agent_agenda_item_last_success_timestamp_seconds",
        "Unix epoch of each agenda item's last successful run.",
        ["name"],
    )
    agent_agenda_items_registered = prometheus_client.Gauge(
        "agent_agenda_items_registered",
        "Number of currently registered agenda items.",
    )
    agent_agenda_reloads_total = prometheus_client.Counter(
        "agent_agenda_reloads_total",
        "Total agenda file-change reload events.",
    )
    agent_agenda_running_items = prometheus_client.Gauge(
        "agent_agenda_running_items",
        "Number of agenda items currently executing.",
    )
    agent_agenda_runs_total = prometheus_client.Counter(
        "agent_agenda_runs_total",
        "Total agenda item executions by name and outcome.",
        ["name", "status"],
    )
    agent_agenda_skips_total = prometheus_client.Counter(
        "agent_agenda_skips_total",
        "Total agenda item skips due to previous run still in progress.",
        ["name"],
    )
    agent_bus_consumer_idle_seconds = prometheus_client.Histogram(
        "agent_bus_consumer_idle_seconds",
        "Idle time between consecutive bus worker processing cycles.",
    )
    agent_bus_dedup_total = prometheus_client.Counter(
        "agent_bus_dedup_total",
        "Total messages dropped by try_send() due to a pending message of the same kind.",
        ["kind"],
    )
    agent_bus_error_processing_duration_seconds = prometheus_client.Histogram(
        "agent_bus_error_processing_duration_seconds",
        "Wall-clock seconds for bus messages that end in error.",
        ["kind"],
    )
    agent_bus_errors_total = prometheus_client.Counter(
        "agent_bus_errors_total",
        "Total unhandled errors in the bus worker.",
    )
    agent_bus_last_processed_timestamp_seconds = prometheus_client.Gauge(
        "agent_bus_last_processed_timestamp_seconds",
        "Unix epoch of the most recent message processed by the bus worker.",
    )
    agent_bus_processing_duration_seconds = prometheus_client.Histogram(
        "agent_bus_processing_duration_seconds",
        "End-to-end processing time for each bus message.",
        ["kind"],
    )
    agent_info = prometheus_client.Info(
        "agent",
        "Static agent metadata.",
    )
    agent_bus_messages_total = prometheus_client.Counter(
        "agent_bus_messages_total",
        "Total messages processed through the message bus.",
        ["kind"],
    )
    agent_bus_pending_kinds = prometheus_client.Gauge(
        "agent_bus_pending_kinds",
        "Number of distinct message kinds currently queued in the bus.",
    )
    agent_bus_queue_depth = prometheus_client.Gauge(
        "agent_bus_queue_depth",
        "Current depth of the message bus queue.",
    )
    agent_bus_wait_seconds = prometheus_client.Histogram(
        "agent_bus_wait_seconds",
        "Seconds a message waited in the bus queue before processing.",
        ["kind"],
    )
    agent_concurrent_queries = prometheus_client.Gauge(
        "agent_concurrent_queries",
        "Number of run() calls currently in flight.",
    )
    agent_context_exhaustion_total = prometheus_client.Counter(
        "agent_context_exhaustion_total",
        "Total context window exhaustion events (usage >= 100%).",
    )
    agent_context_tokens = prometheus_client.Histogram(
        "agent_context_tokens",
        "Absolute token count from get_context_usage() per SDK turn.",
    )
    agent_context_tokens_remaining = prometheus_client.Histogram(
        "agent_context_tokens_remaining",
        "Remaining token budget (maxTokens - totalTokens) per get_context_usage() call.",
        buckets=(1000, 5000, 10000, 25000, 50000, 100000, 150000),
    )
    agent_context_usage_percent = prometheus_client.Histogram(
        "agent_context_usage_percent",
        "Context window utilization percentage per SDK turn.",
        buckets=(50, 70, 80, 90, 95, 99, 100),
    )
    agent_context_warnings_total = prometheus_client.Counter(
        "agent_context_warnings_total",
        "Total context usage threshold warnings.",
    )
    agent_empty_responses_total = prometheus_client.Counter(
        "agent_empty_responses_total",
        "Total tasks that produced no text output.",
    )
    agent_event_loop_lag_seconds = prometheus_client.Histogram(
        "agent_event_loop_lag_seconds",
        "Excess delay beyond expected sleep duration, measuring asyncio event loop congestion.",
    )
    agent_file_watcher_restarts_total = prometheus_client.Counter(
        "agent_file_watcher_restarts_total",
        "Total file watcher restart events due to missing or deleted directory.",
        ["watcher"],
    )
    agent_heartbeat_duration_seconds = prometheus_client.Histogram(
        "agent_heartbeat_duration_seconds",
        "Wall-clock seconds from heartbeat firing to response received.",
    )
    agent_heartbeat_error_duration_seconds = prometheus_client.Histogram(
        "agent_heartbeat_error_duration_seconds",
        "Wall-clock seconds for heartbeats that end in error.",
    )
    agent_heartbeat_lag_seconds = prometheus_client.Histogram(
        "agent_heartbeat_lag_seconds",
        "Delay between scheduled and actual heartbeat execution start.",
    )
    agent_heartbeat_last_error_timestamp_seconds = prometheus_client.Gauge(
        "agent_heartbeat_last_error_timestamp_seconds",
        "Unix epoch of the most recent failed heartbeat.",
    )
    agent_heartbeat_last_run_timestamp_seconds = prometheus_client.Gauge(
        "agent_heartbeat_last_run_timestamp_seconds",
        "Unix epoch of the most recent heartbeat execution, regardless of outcome.",
    )
    agent_heartbeat_last_success_timestamp_seconds = prometheus_client.Gauge(
        "agent_heartbeat_last_success_timestamp_seconds",
        "Unix epoch of the most recent successful heartbeat.",
    )
    agent_heartbeat_load_errors_total = prometheus_client.Counter(
        "agent_heartbeat_load_errors_total",
        "Total heartbeat config load/parse failures.",
    )
    agent_heartbeat_runs_total = prometheus_client.Counter(
        "agent_heartbeat_runs_total",
        "Total heartbeat executions by outcome.",
        ["status"],
    )
    agent_heartbeat_reloads_total = prometheus_client.Counter(
        "agent_heartbeat_reloads_total",
        "Total HEARTBEAT.md file-change reload events.",
    )
    agent_health_checks_total = prometheus_client.Counter(
        "agent_health_checks_total",
        "Total HTTP health endpoint hits by probe type.",
        ["probe"],
    )
    agent_heartbeat_skips_total = prometheus_client.Counter(
        "agent_heartbeat_skips_total",
        "Total heartbeat skips due to previous heartbeat still pending.",
    )
    agent_log_bytes_total = prometheus_client.Counter(
        "agent_log_bytes_total",
        "Total bytes written by the logging subsystem.",
        ["logger"],
    )
    agent_log_entries_total = prometheus_client.Counter(
        "agent_log_entries_total",
        "Total log entries written by logger type.",
        ["logger"],
    )
    agent_log_write_errors_total = prometheus_client.Counter(
        "agent_log_write_errors_total",
        "Total I/O failures in the conversation/trace logging subsystem.",
    )
    agent_lru_cache_utilization_percent = prometheus_client.Gauge(
        "agent_lru_cache_utilization_percent",
        "LRU session cache utilization as a percentage of MAX_SESSIONS.",
    )
    agent_mcp_config_errors_total = prometheus_client.Counter(
        "agent_mcp_config_errors_total",
        "Total MCP config file parse/load failures.",
    )
    agent_mcp_config_reloads_total = prometheus_client.Counter(
        "agent_mcp_config_reloads_total",
        "Total MCP config file reload events.",
    )
    agent_mcp_servers_active = prometheus_client.Gauge(
        "agent_mcp_servers_active",
        "Number of currently loaded MCP servers.",
    )
    agent_model_requests_total = prometheus_client.Counter(
        "agent_model_requests_total",
        "Total requests per resolved model.",
        ["model"],
    )
    agent_prompt_length_bytes = prometheus_client.Histogram(
        "agent_prompt_length_bytes",
        "Byte length of incoming prompts passed to run().",
    )
    agent_running_tasks = prometheus_client.Gauge(
        "agent_running_tasks",
        "Number of currently in-progress tasks.",
    )
    agent_response_length_bytes = prometheus_client.Histogram(
        "agent_response_length_bytes",
        "Byte length of responses returned by run().",
    )
    agent_sdk_client_errors_total = prometheus_client.Counter(
        "agent_sdk_client_errors_total",
        "Total backend client connection-level failures (setup/teardown).",
        ["backend"],
    )
    agent_sdk_context_fetch_errors_total = prometheus_client.Counter(
        "agent_sdk_context_fetch_errors_total",
        "Total context usage fetch failures.",
        ["backend"],
    )
    agent_sdk_errors_total = prometheus_client.Counter(
        "agent_sdk_errors_total",
        "Total stderr/error lines emitted by the backend subprocess.",
        ["backend"],
    )
    agent_sdk_tokens_per_query = prometheus_client.Histogram(
        "agent_sdk_tokens_per_query",
        "Aggregate token count per run_query() invocation.",
        ["backend"],
    )
    agent_sdk_tool_call_input_size_bytes = prometheus_client.Histogram(
        "agent_sdk_tool_call_input_size_bytes",
        "Byte length of each tool call input payload by tool name.",
        ["backend", "tool"],
    )
    agent_sdk_tool_calls_per_query = prometheus_client.Histogram(
        "agent_sdk_tool_calls_per_query",
        "Number of tool calls per run_query() invocation.",
        ["backend"],
        buckets=(0, 1, 2, 5, 10, 20, 50, 100, 200),
    )
    agent_sdk_tool_duration_seconds = prometheus_client.Histogram(
        "agent_sdk_tool_duration_seconds",
        "Wall-clock seconds per tool call.",
        ["backend", "tool"],
    )
    agent_sdk_turns_per_query = prometheus_client.Histogram(
        "agent_sdk_turns_per_query",
        "Number of assistant turns per run_query() invocation.",
        ["backend"],
        buckets=(1, 2, 3, 5, 10, 20, 50, 100),
    )
    agent_sdk_tool_calls_total = prometheus_client.Counter(
        "agent_sdk_tool_calls_total",
        "Total tool calls by tool name.",
        ["backend", "tool"],
    )
    agent_sdk_tool_errors_total = prometheus_client.Counter(
        "agent_sdk_tool_errors_total",
        "Total tool execution errors by tool name.",
        ["backend", "tool"],
    )
    agent_sdk_tool_result_size_bytes = prometheus_client.Histogram(
        "agent_sdk_tool_result_size_bytes",
        "Byte length of each tool result by tool name.",
        ["backend", "tool"],
    )
    agent_sdk_messages_per_query = prometheus_client.Histogram(
        "agent_sdk_messages_per_query",
        "Number of backend messages received per run_query() call.",
        ["backend"],
        buckets=(1, 2, 5, 10, 20, 50, 100, 200),
    )
    agent_sdk_result_errors_total = prometheus_client.Counter(
        "agent_sdk_result_errors_total",
        "Total backend result errors returned during run_query().",
        ["backend"],
    )
    agent_sdk_session_duration_seconds = prometheus_client.Histogram(
        "agent_sdk_session_duration_seconds",
        "Backend session/connection lifetime in seconds.",
        ["backend"],
    )
    agent_sdk_subprocess_spawn_duration_seconds = prometheus_client.Histogram(
        "agent_sdk_subprocess_spawn_duration_seconds",
        "Time to initialize the backend client/subprocess.",
        ["backend"],
    )
    agent_sdk_query_duration_seconds = prometheus_client.Histogram(
        "agent_sdk_query_duration_seconds",
        "Raw backend query time in seconds inside run_query().",
        ["backend"],
    )
    agent_sdk_query_error_duration_seconds = prometheus_client.Histogram(
        "agent_sdk_query_error_duration_seconds",
        "Wall-clock seconds for run_query() calls that end in error.",
        ["backend"],
    )
    agent_sdk_time_to_first_message_seconds = prometheus_client.Histogram(
        "agent_sdk_time_to_first_message_seconds",
        "Seconds from query submission to the first response message.",
        ["backend"],
    )
    agent_startup_duration_seconds = prometheus_client.Gauge(
        "agent_startup_duration_seconds",
        "Time from process start to ready state in seconds.",
    )
    agent_stderr_lines_per_task = prometheus_client.Histogram(
        "agent_stderr_lines_per_task",
        "Number of SDK stderr lines captured per run() invocation.",
        buckets=(0, 1, 2, 5, 10, 20, 50, 100),
    )
    agent_session_age_seconds = prometheus_client.Histogram(
        "agent_session_age_seconds",
        "Seconds since last use when a session is evicted from the LRU cache.",
    )
    agent_session_idle_seconds = prometheus_client.Histogram(
        "agent_session_idle_seconds",
        "Seconds a session was idle before being resumed.",
    )
    agent_session_evictions_total = prometheus_client.Counter(
        "agent_session_evictions_total",
        "Total session evictions due to LRU cap.",
    )
    agent_session_starts_total = prometheus_client.Counter(
        "agent_session_starts_total",
        "Total session starts by type.",
        ["type"],
    )
    agent_task_cancellations_total = prometheus_client.Counter(
        "agent_task_cancellations_total",
        "Total task cancellation requests.",
    )
    agent_task_duration_seconds = prometheus_client.Histogram(
        "agent_task_duration_seconds",
        "Duration of agent tasks in seconds.",
    )
    agent_task_error_duration_seconds = prometheus_client.Histogram(
        "agent_task_error_duration_seconds",
        "Wall-clock seconds for tasks that end in error or timeout.",
    )
    agent_task_last_error_timestamp_seconds = prometheus_client.Gauge(
        "agent_task_last_error_timestamp_seconds",
        "Unix epoch of the most recent failed task execution.",
    )
    agent_task_last_success_timestamp_seconds = prometheus_client.Gauge(
        "agent_task_last_success_timestamp_seconds",
        "Unix epoch of the most recent successful task execution.",
    )
    agent_task_timeout_headroom_seconds = prometheus_client.Histogram(
        "agent_task_timeout_headroom_seconds",
        "Remaining timeout budget when a task completes successfully.",
    )
    agent_uptime_seconds = prometheus_client.Gauge(
        "agent_uptime_seconds",
        "Agent uptime in seconds, computed on each Prometheus scrape.",
    )
    agent_task_restarts_total = prometheus_client.Counter(
        "agent_task_restarts_total",
        "Total worker restarts by the _guarded() loop after an unexpected exception.",
        ["task"],
    )
    agent_task_retries_total = prometheus_client.Counter(
        "agent_task_retries_total",
        "Total task retries due to session already in use.",
    )
    agent_tasks_total = prometheus_client.Counter(
        "agent_tasks_total",
        "Total agent tasks processed by outcome.",
        ["status"],
    )
    agent_tasks_with_stderr_total = prometheus_client.Counter(
        "agent_tasks_with_stderr_total",
        "Total task executions that produced any SDK stderr output.",
    )
    agent_text_blocks_per_query = prometheus_client.Histogram(
        "agent_text_blocks_per_query",
        "Number of text blocks returned per run_query() invocation.",
        buckets=(0, 1, 2, 5, 10, 20, 50, 100),
    )
    agent_watcher_events_total = prometheus_client.Counter(
        "agent_watcher_events_total",
        "Total raw file-system change events detected by each watcher.",
        ["watcher"],
    )
