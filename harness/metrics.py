"""Prometheus metrics for the autonomous agent."""

import os

import prometheus_client

_enabled = bool(os.environ.get("METRICS_ENABLED"))

agent_a2a_last_request_timestamp_seconds: prometheus_client.Gauge | None = None
agent_a2a_request_duration_seconds: prometheus_client.Histogram | None = None
agent_a2a_requests_total: prometheus_client.Counter | None = None
agent_a2a_traces_received_total: prometheus_client.Counter | None = None
agent_up: prometheus_client.Gauge | None = None
agent_active_sessions: prometheus_client.Gauge | None = None
agent_job_checkpoint_stale_total: prometheus_client.Counter | None = None
agent_checkpoint_write_errors_total: prometheus_client.Counter | None = None
agent_job_duration_seconds: prometheus_client.Histogram | None = None
agent_job_error_duration_seconds: prometheus_client.Histogram | None = None
agent_job_lag_seconds: prometheus_client.Histogram | None = None
agent_job_parse_errors_total: prometheus_client.Counter | None = None
agent_job_item_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
agent_job_item_last_run_timestamp_seconds: prometheus_client.Gauge | None = None
agent_job_item_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
agent_job_items_registered: prometheus_client.Gauge | None = None
agent_job_reloads_total: prometheus_client.Counter | None = None
agent_job_running_items: prometheus_client.Gauge | None = None
agent_job_runs_total: prometheus_client.Counter | None = None
agent_job_skips_total: prometheus_client.Counter | None = None
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
agent_model_requests_total: prometheus_client.Counter | None = None
agent_prompt_length_bytes: prometheus_client.Histogram | None = None
agent_running_tasks: prometheus_client.Gauge | None = None
agent_response_length_bytes: prometheus_client.Histogram | None = None
agent_startup_duration_seconds: prometheus_client.Gauge | None = None
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
agent_tasks_total: prometheus_client.Counter | None = None
agent_watcher_events_total: prometheus_client.Counter | None = None
agent_sched_task_checkpoint_stale_total: prometheus_client.Counter | None = None
agent_sched_task_duration_seconds: prometheus_client.Histogram | None = None
agent_sched_task_error_duration_seconds: prometheus_client.Histogram | None = None
agent_sched_task_item_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
agent_sched_task_item_last_run_timestamp_seconds: prometheus_client.Gauge | None = None
agent_sched_task_item_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
agent_sched_task_items_registered: prometheus_client.Gauge | None = None
agent_sched_task_lag_seconds: prometheus_client.Histogram | None = None
agent_sched_task_parse_errors_total: prometheus_client.Counter | None = None
agent_sched_task_reloads_total: prometheus_client.Counter | None = None
agent_sched_task_running_items: prometheus_client.Gauge | None = None
agent_sched_task_runs_total: prometheus_client.Counter | None = None
agent_sched_task_skips_total: prometheus_client.Counter | None = None
agent_triggers_requests_total: prometheus_client.Counter | None = None
agent_adhoc_fires_total: prometheus_client.Counter | None = None
agent_triggers_parse_errors_total: prometheus_client.Counter | None = None
agent_triggers_reloads_total: prometheus_client.Counter | None = None
agent_triggers_items_registered: prometheus_client.Gauge | None = None
agent_continuation_parse_errors_total: prometheus_client.Counter | None = None
agent_continuation_reloads_total: prometheus_client.Counter | None = None
agent_continuation_items_registered: prometheus_client.Gauge | None = None
agent_continuation_runs_total: prometheus_client.Counter | None = None
agent_continuation_fires_total: prometheus_client.Counter | None = None
agent_continuation_throttled_total: prometheus_client.Counter | None = None
agent_continuation_fanin_evictions_total: prometheus_client.Counter | None = None
agent_webhooks_delivery_total: prometheus_client.Counter | None = None
agent_webhooks_delivery_shed_total: prometheus_client.Counter | None = None
agent_webhooks_parse_errors_total: prometheus_client.Counter | None = None
agent_webhooks_reloads_total: prometheus_client.Counter | None = None
agent_webhooks_items_registered: prometheus_client.Gauge | None = None
agent_consensus_runs_total: prometheus_client.Counter | None = None
agent_consensus_backend_errors_total: prometheus_client.Counter | None = None
agent_metrics_backend_fetch_errors_total: prometheus_client.Counter | None = None
agent_backend_proxy_fetch_errors_total: prometheus_client.Counter | None = None
agent_background_tasks: prometheus_client.Gauge | None = None
agent_background_tasks_shed_total: prometheus_client.Counter | None = None
agent_background_tasks_timeout_total: prometheus_client.Counter | None = None
agent_backend_reachable: prometheus_client.Gauge | None = None
agent_a2a_backend_requests_total: prometheus_client.Counter | None = None
agent_a2a_backend_request_duration_seconds: prometheus_client.Histogram | None = None


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
    agent_a2a_traces_received_total = prometheus_client.Counter(
        "agent_a2a_traces_received_total",
        "Total inbound A2A requests by trace-context provenance — labelled "
        "has_inbound=true when the caller passed a valid W3C traceparent "
        "header, false when the harness minted a fresh context (#468).",
        ["has_inbound"],
    )
    agent_up = prometheus_client.Gauge("agent_up", "Agent is running", ["agent"])
    agent_active_sessions = prometheus_client.Gauge(
        "agent_active_sessions",
        "Number of active sessions tracked in the LRU cache.",
    )
    agent_job_checkpoint_stale_total = prometheus_client.Counter(
        "agent_job_checkpoint_stale_total",
        "Total stale checkpoint files found during job runner startup scan.",
    )
    agent_checkpoint_write_errors_total = prometheus_client.Counter(
        "agent_checkpoint_write_errors_total",
        "Total job checkpoint I/O failures.",
    )
    agent_job_duration_seconds = prometheus_client.Histogram(
        "agent_job_duration_seconds",
        "Wall-clock seconds per job execution.",
        ["name"],
    )
    agent_job_error_duration_seconds = prometheus_client.Histogram(
        "agent_job_error_duration_seconds",
        "Wall-clock seconds for jobs that end in error.",
        ["name"],
    )
    agent_job_lag_seconds = prometheus_client.Histogram(
        "agent_job_lag_seconds",
        "Delay between scheduled and actual job execution start.",
    )
    agent_job_parse_errors_total = prometheus_client.Counter(
        "agent_job_parse_errors_total",
        "Total job file parse failures.",
    )
    agent_job_item_last_error_timestamp_seconds = prometheus_client.Gauge(
        "agent_job_item_last_error_timestamp_seconds",
        "Unix epoch of each job's last failed run.",
        ["name"],
    )
    agent_job_item_last_run_timestamp_seconds = prometheus_client.Gauge(
        "agent_job_item_last_run_timestamp_seconds",
        "Unix epoch of each job's most recent execution, regardless of outcome.",
        ["name"],
    )
    agent_job_item_last_success_timestamp_seconds = prometheus_client.Gauge(
        "agent_job_item_last_success_timestamp_seconds",
        "Unix epoch of each job's last successful run.",
        ["name"],
    )
    agent_job_items_registered = prometheus_client.Gauge(
        "agent_job_items_registered",
        "Number of currently registered jobs.",
    )
    agent_job_reloads_total = prometheus_client.Counter(
        "agent_job_reloads_total",
        "Total job file-change reload events.",
    )
    agent_job_running_items = prometheus_client.Gauge(
        "agent_job_running_items",
        "Number of jobs currently executing.",
    )
    agent_job_runs_total = prometheus_client.Counter(
        "agent_job_runs_total",
        "Total job executions by name and outcome.",
        ["name", "status"],
    )
    agent_job_skips_total = prometheus_client.Counter(
        "agent_job_skips_total",
        "Total job skips due to previous run still in progress.",
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
    agent_startup_duration_seconds = prometheus_client.Gauge(
        "agent_startup_duration_seconds",
        "Time from process start to ready state in seconds.",
    )
    agent_session_age_seconds = prometheus_client.Histogram(
        "agent_session_age_seconds",
        "Seconds since last use when a session is evicted from the LRU cache.",
        buckets=(60, 300, 900, 1800, 3600, 7200, 14400, 28800, 86400),
    )
    agent_session_idle_seconds = prometheus_client.Histogram(
        "agent_session_idle_seconds",
        "Seconds a session was idle before being resumed.",
        buckets=(60, 300, 900, 1800, 3600, 7200, 14400, 28800, 86400),
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
    agent_tasks_total = prometheus_client.Counter(
        "agent_tasks_total",
        "Total agent tasks processed by outcome.",
        ["status"],
    )
    agent_watcher_events_total = prometheus_client.Counter(
        "agent_watcher_events_total",
        "Total raw file-system change events detected by each watcher.",
        ["watcher"],
    )
    agent_sched_task_checkpoint_stale_total = prometheus_client.Counter(
        "agent_sched_task_checkpoint_stale_total",
        "Total stale checkpoint files found during task runner startup scan.",
    )
    agent_sched_task_duration_seconds = prometheus_client.Histogram(
        "agent_sched_task_duration_seconds",
        "Wall-clock seconds per scheduled task execution.",
        ["name"],
    )
    agent_sched_task_error_duration_seconds = prometheus_client.Histogram(
        "agent_sched_task_error_duration_seconds",
        "Wall-clock seconds for scheduled tasks that end in error.",
        ["name"],
    )
    agent_sched_task_item_last_error_timestamp_seconds = prometheus_client.Gauge(
        "agent_sched_task_item_last_error_timestamp_seconds",
        "Unix epoch of each scheduled task's last failed run.",
        ["name"],
    )
    agent_sched_task_item_last_run_timestamp_seconds = prometheus_client.Gauge(
        "agent_sched_task_item_last_run_timestamp_seconds",
        "Unix epoch of each scheduled task's most recent execution, regardless of outcome.",
        ["name"],
    )
    agent_sched_task_item_last_success_timestamp_seconds = prometheus_client.Gauge(
        "agent_sched_task_item_last_success_timestamp_seconds",
        "Unix epoch of each scheduled task's last successful run.",
        ["name"],
    )
    agent_sched_task_items_registered = prometheus_client.Gauge(
        "agent_sched_task_items_registered",
        "Number of currently registered scheduled tasks.",
    )
    agent_sched_task_lag_seconds = prometheus_client.Histogram(
        "agent_sched_task_lag_seconds",
        "Delay between scheduled window open and actual task execution start.",
    )
    agent_sched_task_parse_errors_total = prometheus_client.Counter(
        "agent_sched_task_parse_errors_total",
        "Total scheduled task file parse failures.",
    )
    agent_sched_task_reloads_total = prometheus_client.Counter(
        "agent_sched_task_reloads_total",
        "Total scheduled task file-change reload events.",
    )
    agent_sched_task_running_items = prometheus_client.Gauge(
        "agent_sched_task_running_items",
        "Number of scheduled tasks currently executing.",
    )
    agent_sched_task_runs_total = prometheus_client.Counter(
        "agent_sched_task_runs_total",
        "Total scheduled task executions by name and outcome.",
        ["name", "status"],
    )
    agent_sched_task_skips_total = prometheus_client.Counter(
        "agent_sched_task_skips_total",
        "Total scheduled task skips due to previous run still in progress.",
        ["name"],
    )
    agent_triggers_requests_total = prometheus_client.Counter(
        "agent_triggers_requests_total",
        "Trigger endpoint HTTP requests by method and response code.",
        ["method", "code"],
    )
    agent_adhoc_fires_total = prometheus_client.Counter(
        "agent_adhoc_fires_total",
        "Ad-hoc run endpoint fire attempts for scheduled kinds (jobs, tasks, heartbeat).",
        ["kind", "name", "code"],
    )
    agent_triggers_parse_errors_total = prometheus_client.Counter(
        "agent_triggers_parse_errors_total",
        "Total trigger file parse failures.",
    )
    agent_triggers_reloads_total = prometheus_client.Counter(
        "agent_triggers_reloads_total",
        "Total trigger file-change reload events.",
    )
    agent_triggers_items_registered = prometheus_client.Gauge(
        "agent_triggers_items_registered",
        "Number of currently registered trigger endpoints.",
    )
    agent_continuation_parse_errors_total = prometheus_client.Counter(
        "agent_continuation_parse_errors_total",
        "Total continuation file parse failures.",
    )
    agent_continuation_reloads_total = prometheus_client.Counter(
        "agent_continuation_reloads_total",
        "Total continuation file-change reload events.",
    )
    agent_continuation_items_registered = prometheus_client.Gauge(
        "agent_continuation_items_registered",
        "Number of currently registered continuations.",
    )
    agent_continuation_runs_total = prometheus_client.Counter(
        "agent_continuation_runs_total",
        "Total continuation executions by name and outcome.",
        ["name", "status"],
    )
    agent_continuation_fires_total = prometheus_client.Counter(
        "agent_continuation_fires_total",
        "Total continuation firings by upstream kind.",
        ["upstream_kind"],
    )
    agent_continuation_throttled_total = prometheus_client.Counter(
        "agent_continuation_throttled_total",
        "Total continuation firings skipped due to max_concurrent_fires throttle.",
        ["name"],
    )
    agent_continuation_fanin_evictions_total = prometheus_client.Counter(
        "agent_continuation_fanin_evictions_total",
        "Total partial fan-in state entries evicted after TTL expiry.",
        ["name"],
    )
    agent_webhooks_delivery_total = prometheus_client.Counter(
        "agent_webhooks_delivery_total",
        "Outbound webhook delivery attempts by result and subscription name.",
        ["result", "subscription"],
    )
    agent_webhooks_delivery_shed_total = prometheus_client.Counter(
        "agent_webhooks_delivery_shed_total",
        "Total webhook delivery tasks shed because the concurrent delivery cap was reached.",
        ["subscription"],
    )
    agent_webhooks_parse_errors_total = prometheus_client.Counter(
        "agent_webhooks_parse_errors_total",
        "Total webhook file parse failures.",
    )
    agent_webhooks_reloads_total = prometheus_client.Counter(
        "agent_webhooks_reloads_total",
        "Total webhook file-change reload events.",
    )
    agent_webhooks_items_registered = prometheus_client.Gauge(
        "agent_webhooks_items_registered",
        "Number of currently registered webhook subscriptions.",
    )
    agent_consensus_runs_total = prometheus_client.Counter(
        "agent_consensus_runs_total",
        "Total consensus-mode executions by mode and outcome.",
        ["mode", "status"],
    )
    agent_consensus_backend_errors_total = prometheus_client.Counter(
        "agent_consensus_backend_errors_total",
        "Total backend failures during consensus fan-out.",
    )
    agent_metrics_backend_fetch_errors_total = prometheus_client.Counter(
        "agent_metrics_backend_fetch_errors_total",
        "Total failures when fetching /metrics from a backend during aggregation.",
        ["backend"],
    )
    agent_backend_proxy_fetch_errors_total = prometheus_client.Counter(
        "agent_backend_proxy_fetch_errors_total",
        "Total failures when fetching /conversations or /trace from a backend "
        "during proxy aggregation (#579). endpoint=\"conversations\"|\"trace\".",
        ["backend", "endpoint"],
    )
    agent_background_tasks = prometheus_client.Gauge(
        "agent_background_tasks",
        "Number of background asyncio tasks currently in flight (webhooks, continuations, etc.).",
    )
    agent_background_tasks_shed_total = prometheus_client.Counter(
        "agent_background_tasks_shed_total",
        "Total background tasks shed because the executor's in-flight cap was reached.",
        ["source"],
    )
    agent_background_tasks_timeout_total = prometheus_client.Counter(
        "agent_background_tasks_timeout_total",
        "Total background tasks cancelled because they exceeded ON_PROMPT_COMPLETED_TIMEOUT.",
        ["source"],
    )
    agent_backend_reachable = prometheus_client.Gauge(
        "agent_backend_reachable",
        "Whether a configured backend responded OK on its last /health probe "
        "from the harness health_ready sweep (#619). 1=reachable, 0=unreachable.",
        ["backend"],
    )
    agent_a2a_backend_requests_total = prometheus_client.Counter(
        "agent_a2a_backend_requests_total",
        "Total outbound A2A requests issued by the harness to configured "
        "backends, bucketed by coarse result (#622). Result is one of "
        "ok | error_status | error_connection | error_timeout — raw HTTP "
        "status codes are intentionally NOT in the label set to bound "
        "cardinality.",
        ["backend", "result"],
    )
    agent_a2a_backend_request_duration_seconds = prometheus_client.Histogram(
        "agent_a2a_backend_request_duration_seconds",
        "Wall-clock seconds for outbound A2A requests from the harness to "
        "each configured backend (#622).",
        ["backend"],
    )

