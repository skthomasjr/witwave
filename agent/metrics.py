"""Prometheus metrics for the autonomous agent."""

import os

import prometheus_client

_enabled = bool(os.environ.get("METRICS_ENABLED"))

agent_a2a_request_duration_seconds: prometheus_client.Histogram | None = None
agent_a2a_requests_total: prometheus_client.Counter | None = None
agent_up: prometheus_client.Gauge | None = None
agent_active_sessions: prometheus_client.Gauge | None = None
agent_agenda_checkpoint_stale_total: prometheus_client.Counter | None = None
agent_checkpoint_write_errors_total: prometheus_client.Counter | None = None
agent_agenda_duration_seconds: prometheus_client.Histogram | None = None
agent_agenda_lag_seconds: prometheus_client.Histogram | None = None
agent_agenda_parse_errors_total: prometheus_client.Counter | None = None
agent_agenda_items_registered: prometheus_client.Gauge | None = None
agent_agenda_reloads_total: prometheus_client.Counter | None = None
agent_agenda_running_items: prometheus_client.Gauge | None = None
agent_agenda_runs_total: prometheus_client.Counter | None = None
agent_agenda_skips_total: prometheus_client.Counter | None = None
agent_bus_errors_total: prometheus_client.Counter | None = None
agent_bus_processing_duration_seconds: prometheus_client.Histogram | None = None
agent_info: prometheus_client.Info | None = None
agent_bus_messages_total: prometheus_client.Counter | None = None
agent_bus_queue_depth: prometheus_client.Gauge | None = None
agent_bus_wait_seconds: prometheus_client.Histogram | None = None
agent_concurrent_queries: prometheus_client.Gauge | None = None
agent_context_tokens: prometheus_client.Histogram | None = None
agent_context_usage_percent: prometheus_client.Histogram | None = None
agent_context_warnings_total: prometheus_client.Counter | None = None
agent_empty_responses_total: prometheus_client.Counter | None = None
agent_file_watcher_restarts_total: prometheus_client.Counter | None = None
agent_heartbeat_duration_seconds: prometheus_client.Histogram | None = None
agent_heartbeat_lag_seconds: prometheus_client.Histogram | None = None
agent_heartbeat_load_errors_total: prometheus_client.Counter | None = None
agent_heartbeat_runs_total: prometheus_client.Counter | None = None
agent_heartbeat_reloads_total: prometheus_client.Counter | None = None
agent_health_checks_total: prometheus_client.Counter | None = None
agent_heartbeat_skips_total: prometheus_client.Counter | None = None
agent_log_write_errors_total: prometheus_client.Counter | None = None
agent_mcp_config_errors_total: prometheus_client.Counter | None = None
agent_mcp_config_reloads_total: prometheus_client.Counter | None = None
agent_mcp_servers_active: prometheus_client.Gauge | None = None
agent_model_requests_total: prometheus_client.Counter | None = None
agent_prompt_length_bytes: prometheus_client.Histogram | None = None
agent_running_tasks: prometheus_client.Gauge | None = None
agent_response_length_bytes: prometheus_client.Histogram | None = None
agent_sdk_context_fetch_errors_total: prometheus_client.Counter | None = None
agent_sdk_errors_total: prometheus_client.Counter | None = None
agent_sdk_tool_calls_per_query: prometheus_client.Histogram | None = None
agent_sdk_tool_calls_total: prometheus_client.Counter | None = None
agent_sdk_tool_errors_total: prometheus_client.Counter | None = None
agent_sdk_messages_per_query: prometheus_client.Histogram | None = None
agent_sdk_result_errors_total: prometheus_client.Counter | None = None
agent_sdk_query_duration_seconds: prometheus_client.Histogram | None = None
agent_startup_duration_seconds: prometheus_client.Gauge | None = None
agent_stderr_lines_per_task: prometheus_client.Histogram | None = None
agent_session_age_seconds: prometheus_client.Histogram | None = None
agent_session_evictions_total: prometheus_client.Counter | None = None
agent_session_starts_total: prometheus_client.Counter | None = None
agent_task_cancellations_total: prometheus_client.Counter | None = None
agent_task_duration_seconds: prometheus_client.Histogram | None = None
agent_uptime_seconds: prometheus_client.Gauge | None = None
agent_task_retries_total: prometheus_client.Counter | None = None
agent_tasks_total: prometheus_client.Counter | None = None
agent_tasks_with_stderr_total: prometheus_client.Counter | None = None
agent_text_blocks_per_query: prometheus_client.Histogram | None = None
agent_watcher_events_total: prometheus_client.Counter | None = None

if _enabled:
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
    agent_agenda_lag_seconds = prometheus_client.Histogram(
        "agent_agenda_lag_seconds",
        "Delay between scheduled and actual agenda item execution start.",
    )
    agent_agenda_parse_errors_total = prometheus_client.Counter(
        "agent_agenda_parse_errors_total",
        "Total agenda file parse failures.",
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
    agent_bus_errors_total = prometheus_client.Counter(
        "agent_bus_errors_total",
        "Total unhandled errors in the bus worker.",
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
    agent_bus_queue_depth = prometheus_client.Gauge(
        "agent_bus_queue_depth",
        "Current depth of the message bus queue.",
    )
    agent_bus_wait_seconds = prometheus_client.Histogram(
        "agent_bus_wait_seconds",
        "Seconds a message waited in the bus queue before processing.",
    )
    agent_concurrent_queries = prometheus_client.Gauge(
        "agent_concurrent_queries",
        "Number of run() calls currently in flight.",
    )
    agent_context_tokens = prometheus_client.Histogram(
        "agent_context_tokens",
        "Absolute token count from get_context_usage() per SDK turn.",
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
    agent_file_watcher_restarts_total = prometheus_client.Counter(
        "agent_file_watcher_restarts_total",
        "Total file watcher restart events due to missing or deleted directory.",
        ["watcher"],
    )
    agent_heartbeat_duration_seconds = prometheus_client.Histogram(
        "agent_heartbeat_duration_seconds",
        "Wall-clock seconds from heartbeat firing to response received.",
    )
    agent_heartbeat_lag_seconds = prometheus_client.Histogram(
        "agent_heartbeat_lag_seconds",
        "Delay between scheduled and actual heartbeat execution start.",
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
    agent_log_write_errors_total = prometheus_client.Counter(
        "agent_log_write_errors_total",
        "Total I/O failures in the conversation/trace logging subsystem.",
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
    agent_sdk_context_fetch_errors_total = prometheus_client.Counter(
        "agent_sdk_context_fetch_errors_total",
        "Total get_context_usage() call failures.",
    )
    agent_sdk_errors_total = prometheus_client.Counter(
        "agent_sdk_errors_total",
        "Total stderr lines emitted by the Claude SDK subprocess.",
    )
    agent_sdk_tool_calls_per_query = prometheus_client.Histogram(
        "agent_sdk_tool_calls_per_query",
        "Number of tool calls per run_query() invocation.",
        buckets=(0, 1, 2, 5, 10, 20, 50, 100, 200),
    )
    agent_sdk_tool_calls_total = prometheus_client.Counter(
        "agent_sdk_tool_calls_total",
        "Total tool calls by tool name.",
        ["tool"],
    )
    agent_sdk_tool_errors_total = prometheus_client.Counter(
        "agent_sdk_tool_errors_total",
        "Total tool execution errors by tool name.",
        ["tool"],
    )
    agent_sdk_messages_per_query = prometheus_client.Histogram(
        "agent_sdk_messages_per_query",
        "Number of SDK messages received per run_query() call.",
        buckets=(1, 2, 5, 10, 20, 50, 100, 200),
    )
    agent_sdk_result_errors_total = prometheus_client.Counter(
        "agent_sdk_result_errors_total",
        "Total SDK ResultMessage errors returned during run_query().",
    )
    agent_sdk_query_duration_seconds = prometheus_client.Histogram(
        "agent_sdk_query_duration_seconds",
        "Raw SDK query time in seconds inside run_query().",
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
        "Age of a session in seconds when evicted from the LRU cache.",
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
    agent_uptime_seconds = prometheus_client.Gauge(
        "agent_uptime_seconds",
        "Agent uptime in seconds, computed on each Prometheus scrape.",
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
