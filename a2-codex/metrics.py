"""Prometheus metrics for the a2-codex backend agent."""

import os

import prometheus_client

_enabled = bool(os.environ.get("METRICS_ENABLED"))

# Service-level metrics
a2_up: prometheus_client.Gauge | None = None
a2_info: prometheus_client.Info | None = None
a2_uptime_seconds: prometheus_client.Gauge | None = None
a2_startup_duration_seconds: prometheus_client.Gauge | None = None
a2_event_loop_lag_seconds: prometheus_client.Histogram | None = None
a2_health_checks_total: prometheus_client.Counter | None = None
a2_task_restarts_total: prometheus_client.Counter | None = None

# A2A request metrics
a2_a2a_requests_total: prometheus_client.Counter | None = None
a2_a2a_request_duration_seconds: prometheus_client.Histogram | None = None
a2_a2a_last_request_timestamp_seconds: prometheus_client.Gauge | None = None

# Task execution metrics
a2_tasks_total: prometheus_client.Counter | None = None
a2_task_duration_seconds: prometheus_client.Histogram | None = None
a2_task_error_duration_seconds: prometheus_client.Histogram | None = None
a2_task_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
a2_task_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
a2_task_timeout_headroom_seconds: prometheus_client.Histogram | None = None
a2_task_cancellations_total: prometheus_client.Counter | None = None
a2_running_tasks: prometheus_client.Gauge | None = None
a2_concurrent_queries: prometheus_client.Gauge | None = None

# Session metrics
a2_active_sessions: prometheus_client.Gauge | None = None
a2_session_starts_total: prometheus_client.Counter | None = None
a2_session_evictions_total: prometheus_client.Counter | None = None
a2_session_age_seconds: prometheus_client.Histogram | None = None
a2_session_idle_seconds: prometheus_client.Histogram | None = None
a2_lru_cache_utilization_percent: prometheus_client.Gauge | None = None

# Prompt / response size metrics
a2_prompt_length_bytes: prometheus_client.Histogram | None = None
a2_response_length_bytes: prometheus_client.Histogram | None = None
a2_empty_responses_total: prometheus_client.Counter | None = None

# Model / backend routing metrics
a2_model_requests_total: prometheus_client.Counter | None = None

# Logging subsystem metrics
a2_log_bytes_total: prometheus_client.Counter | None = None
a2_log_entries_total: prometheus_client.Counter | None = None
a2_log_write_errors_total: prometheus_client.Counter | None = None

# SDK metrics
a2_sdk_query_duration_seconds: prometheus_client.Histogram | None = None
a2_sdk_query_error_duration_seconds: prometheus_client.Histogram | None = None
a2_sdk_time_to_first_message_seconds: prometheus_client.Histogram | None = None
a2_sdk_session_duration_seconds: prometheus_client.Histogram | None = None
a2_sdk_messages_per_query: prometheus_client.Histogram | None = None
a2_sdk_turns_per_query: prometheus_client.Histogram | None = None
a2_text_blocks_per_query: prometheus_client.Histogram | None = None

if _enabled:
    a2_up = prometheus_client.Gauge("a2_up", "Backend agent is running", ["agent", "agent_id", "backend"])
    a2_info = prometheus_client.Info("a2", "Static backend agent metadata.")
    a2_uptime_seconds = prometheus_client.Gauge(
        "a2_uptime_seconds",
        "Backend agent uptime in seconds, computed on each Prometheus scrape.",
        ["agent", "agent_id", "backend"],
    )
    a2_startup_duration_seconds = prometheus_client.Gauge(
        "a2_startup_duration_seconds",
        "Time from process start to ready state in seconds.",
        ["agent", "agent_id", "backend"],
    )
    a2_event_loop_lag_seconds = prometheus_client.Histogram(
        "a2_event_loop_lag_seconds",
        "Excess delay beyond expected sleep duration, measuring asyncio event loop congestion.",
        ["agent", "agent_id", "backend"],
    )
    a2_health_checks_total = prometheus_client.Counter(
        "a2_health_checks_total",
        "Total HTTP health endpoint hits by probe type.",
        ["agent", "agent_id", "backend", "probe"],
    )
    a2_task_restarts_total = prometheus_client.Counter(
        "a2_task_restarts_total",
        "Total worker restarts by the _guarded() loop after an unexpected exception.",
        ["agent", "agent_id", "backend", "task"],
    )

    # A2A
    a2_a2a_requests_total = prometheus_client.Counter(
        "a2_a2a_requests_total",
        "Total A2A HTTP requests by outcome.",
        ["agent", "agent_id", "backend", "status"],
    )
    a2_a2a_request_duration_seconds = prometheus_client.Histogram(
        "a2_a2a_request_duration_seconds",
        "Wall-clock duration of each A2A execute() call.",
        ["agent", "agent_id", "backend"],
    )
    a2_a2a_last_request_timestamp_seconds = prometheus_client.Gauge(
        "a2_a2a_last_request_timestamp_seconds",
        "Unix epoch of the most recent A2A request received.",
        ["agent", "agent_id", "backend"],
    )

    # Tasks
    a2_tasks_total = prometheus_client.Counter(
        "a2_tasks_total",
        "Total agent tasks processed by outcome.",
        ["agent", "agent_id", "backend", "status"],
    )
    a2_task_duration_seconds = prometheus_client.Histogram(
        "a2_task_duration_seconds",
        "Duration of agent tasks in seconds.",
        ["agent", "agent_id", "backend"],
    )
    a2_task_error_duration_seconds = prometheus_client.Histogram(
        "a2_task_error_duration_seconds",
        "Wall-clock seconds for tasks that end in error or timeout.",
        ["agent", "agent_id", "backend"],
    )
    a2_task_last_success_timestamp_seconds = prometheus_client.Gauge(
        "a2_task_last_success_timestamp_seconds",
        "Unix epoch of the most recent successful task execution.",
        ["agent", "agent_id", "backend"],
    )
    a2_task_last_error_timestamp_seconds = prometheus_client.Gauge(
        "a2_task_last_error_timestamp_seconds",
        "Unix epoch of the most recent failed task execution.",
        ["agent", "agent_id", "backend"],
    )
    a2_task_timeout_headroom_seconds = prometheus_client.Histogram(
        "a2_task_timeout_headroom_seconds",
        "Remaining timeout budget when a task completes successfully.",
        ["agent", "agent_id", "backend"],
    )
    a2_task_cancellations_total = prometheus_client.Counter(
        "a2_task_cancellations_total",
        "Total task cancellation requests.",
        ["agent", "agent_id", "backend"],
    )
    a2_running_tasks = prometheus_client.Gauge(
        "a2_running_tasks",
        "Number of currently in-progress tasks.",
        ["agent", "agent_id", "backend"],
    )
    a2_concurrent_queries = prometheus_client.Gauge(
        "a2_concurrent_queries",
        "Number of run() calls currently in flight.",
        ["agent", "agent_id", "backend"],
    )

    # Sessions
    a2_active_sessions = prometheus_client.Gauge(
        "a2_active_sessions",
        "Number of active sessions tracked in the LRU cache.",
        ["agent", "agent_id", "backend"],
    )
    a2_session_starts_total = prometheus_client.Counter(
        "a2_session_starts_total",
        "Total session starts by type.",
        ["agent", "agent_id", "backend", "type"],
    )
    a2_session_evictions_total = prometheus_client.Counter(
        "a2_session_evictions_total",
        "Total session evictions due to LRU cap.",
        ["agent", "agent_id", "backend"],
    )
    a2_session_age_seconds = prometheus_client.Histogram(
        "a2_session_age_seconds",
        "Seconds since last use when a session is evicted from the LRU cache.",
        ["agent", "agent_id", "backend"],
        buckets=(60, 300, 900, 1800, 3600, 7200, 14400, 28800, 86400),
    )
    a2_session_idle_seconds = prometheus_client.Histogram(
        "a2_session_idle_seconds",
        "Seconds a session was idle before being resumed.",
        ["agent", "agent_id", "backend"],
        buckets=(60, 300, 900, 1800, 3600, 7200, 14400, 28800, 86400),
    )
    a2_lru_cache_utilization_percent = prometheus_client.Gauge(
        "a2_lru_cache_utilization_percent",
        "LRU session cache utilization as a percentage of MAX_SESSIONS.",
        ["agent", "agent_id", "backend"],
    )

    # Prompt / response
    a2_prompt_length_bytes = prometheus_client.Histogram(
        "a2_prompt_length_bytes",
        "Byte length of incoming prompts passed to run().",
        ["agent", "agent_id", "backend"],
    )
    a2_response_length_bytes = prometheus_client.Histogram(
        "a2_response_length_bytes",
        "Byte length of responses returned by run().",
        ["agent", "agent_id", "backend"],
    )
    a2_empty_responses_total = prometheus_client.Counter(
        "a2_empty_responses_total",
        "Total tasks that produced no text output.",
        ["agent", "agent_id", "backend"],
    )

    # Model routing
    a2_model_requests_total = prometheus_client.Counter(
        "a2_model_requests_total",
        "Total requests per resolved model.",
        ["agent", "agent_id", "backend", "model"],
    )

    # Logging
    a2_log_bytes_total = prometheus_client.Counter(
        "a2_log_bytes_total",
        "Total bytes written by the logging subsystem.",
        ["agent", "agent_id", "backend", "logger"],
    )
    a2_log_entries_total = prometheus_client.Counter(
        "a2_log_entries_total",
        "Total log entries written by logger type.",
        ["agent", "agent_id", "backend", "logger"],
    )
    a2_log_write_errors_total = prometheus_client.Counter(
        "a2_log_write_errors_total",
        "Total I/O failures in the conversation/trace logging subsystem.",
        ["agent", "agent_id", "backend"],
    )

    # SDK
    a2_sdk_query_duration_seconds = prometheus_client.Histogram(
        "a2_sdk_query_duration_seconds",
        "Raw backend query time in seconds inside run_query().",
        ["agent", "agent_id", "backend", "model"],
    )
    a2_sdk_query_error_duration_seconds = prometheus_client.Histogram(
        "a2_sdk_query_error_duration_seconds",
        "Wall-clock seconds for run_query() calls that end in error.",
        ["agent", "agent_id", "backend", "model"],
    )
    a2_sdk_time_to_first_message_seconds = prometheus_client.Histogram(
        "a2_sdk_time_to_first_message_seconds",
        "Seconds from query submission to the first response message.",
        ["agent", "agent_id", "backend", "model"],
    )
    a2_sdk_session_duration_seconds = prometheus_client.Histogram(
        "a2_sdk_session_duration_seconds",
        "Backend session/connection lifetime in seconds.",
        ["agent", "agent_id", "backend", "model"],
    )
    a2_sdk_messages_per_query = prometheus_client.Histogram(
        "a2_sdk_messages_per_query",
        "Number of backend messages received per run_query() call.",
        ["agent", "agent_id", "backend", "model"],
        buckets=(1, 2, 5, 10, 20, 50, 100, 200),
    )
    a2_sdk_turns_per_query = prometheus_client.Histogram(
        "a2_sdk_turns_per_query",
        "Number of assistant turns per run_query() invocation.",
        ["agent", "agent_id", "backend", "model"],
        buckets=(1, 2, 3, 5, 10, 20, 50, 100),
    )
    a2_text_blocks_per_query = prometheus_client.Histogram(
        "a2_text_blocks_per_query",
        "Number of text blocks returned per run_query() invocation.",
        ["agent", "agent_id", "backend", "model"],
        buckets=(0, 1, 2, 5, 10, 20, 50, 100),
    )
