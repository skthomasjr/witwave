"""Prometheus metrics for the autonomous agent."""

import os

import prometheus_client

_enabled = bool(os.environ.get("METRICS_ENABLED"))

harness_a2a_last_request_timestamp_seconds: prometheus_client.Gauge | None = None
harness_a2a_request_duration_seconds: prometheus_client.Histogram | None = None
harness_a2a_requests_total: prometheus_client.Counter | None = None
harness_a2a_traces_received_total: prometheus_client.Counter | None = None
harness_up: prometheus_client.Gauge | None = None
harness_active_sessions: prometheus_client.Gauge | None = None
harness_job_checkpoint_stale_total: prometheus_client.Counter | None = None
harness_checkpoint_write_errors_total: prometheus_client.Counter | None = None
harness_job_duration_seconds: prometheus_client.Histogram | None = None
harness_job_error_duration_seconds: prometheus_client.Histogram | None = None
harness_job_lag_seconds: prometheus_client.Histogram | None = None
harness_job_parse_errors_total: prometheus_client.Counter | None = None
harness_job_item_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
harness_job_item_last_run_timestamp_seconds: prometheus_client.Gauge | None = None
harness_job_item_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
harness_job_items_registered: prometheus_client.Gauge | None = None
harness_job_reloads_total: prometheus_client.Counter | None = None
harness_job_running_items: prometheus_client.Gauge | None = None
harness_job_runs_total: prometheus_client.Counter | None = None
harness_job_skips_total: prometheus_client.Counter | None = None
harness_bus_consumer_idle_seconds: prometheus_client.Histogram | None = None
harness_bus_dedup_total: prometheus_client.Counter | None = None
harness_bus_error_processing_duration_seconds: prometheus_client.Histogram | None = None
harness_bus_errors_total: prometheus_client.Counter | None = None
harness_bus_last_processed_timestamp_seconds: prometheus_client.Gauge | None = None
harness_bus_processing_duration_seconds: prometheus_client.Histogram | None = None
harness_info: prometheus_client.Info | None = None
harness_bus_messages_total: prometheus_client.Counter | None = None
harness_bus_pending_kinds: prometheus_client.Gauge | None = None
harness_bus_queue_depth: prometheus_client.Gauge | None = None
harness_bus_wait_seconds: prometheus_client.Histogram | None = None
harness_concurrent_queries: prometheus_client.Gauge | None = None
harness_empty_responses_total: prometheus_client.Counter | None = None
harness_event_loop_lag_seconds: prometheus_client.Histogram | None = None
harness_file_watcher_restarts_total: prometheus_client.Counter | None = None
harness_heartbeat_duration_seconds: prometheus_client.Histogram | None = None
harness_heartbeat_error_duration_seconds: prometheus_client.Histogram | None = None
harness_heartbeat_lag_seconds: prometheus_client.Histogram | None = None
harness_heartbeat_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
harness_heartbeat_last_run_timestamp_seconds: prometheus_client.Gauge | None = None
harness_heartbeat_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
harness_heartbeat_load_errors_total: prometheus_client.Counter | None = None
harness_heartbeat_runs_total: prometheus_client.Counter | None = None
harness_heartbeat_reloads_total: prometheus_client.Counter | None = None
harness_health_checks_total: prometheus_client.Counter | None = None
harness_heartbeat_skips_total: prometheus_client.Counter | None = None
harness_log_bytes_total: prometheus_client.Counter | None = None
harness_log_entries_total: prometheus_client.Counter | None = None
harness_log_write_errors_total: prometheus_client.Counter | None = None
harness_lru_cache_utilization_percent: prometheus_client.Gauge | None = None
harness_model_requests_total: prometheus_client.Counter | None = None
harness_prompt_length_bytes: prometheus_client.Histogram | None = None
harness_running_tasks: prometheus_client.Gauge | None = None
harness_response_length_bytes: prometheus_client.Histogram | None = None
harness_startup_duration_seconds: prometheus_client.Gauge | None = None
harness_session_age_seconds: prometheus_client.Histogram | None = None
harness_session_idle_seconds: prometheus_client.Histogram | None = None
harness_session_evictions_total: prometheus_client.Counter | None = None
harness_session_starts_total: prometheus_client.Counter | None = None
harness_task_cancellations_total: prometheus_client.Counter | None = None
harness_task_duration_seconds: prometheus_client.Histogram | None = None
harness_task_error_duration_seconds: prometheus_client.Histogram | None = None
harness_task_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
harness_task_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
harness_task_timeout_headroom_seconds: prometheus_client.Histogram | None = None
harness_uptime_seconds: prometheus_client.Gauge | None = None
harness_task_restarts_total: prometheus_client.Counter | None = None
harness_tasks_total: prometheus_client.Counter | None = None
harness_watcher_events_total: prometheus_client.Counter | None = None
harness_sched_task_checkpoint_stale_total: prometheus_client.Counter | None = None
harness_sched_task_duration_seconds: prometheus_client.Histogram | None = None
harness_sched_task_error_duration_seconds: prometheus_client.Histogram | None = None
harness_sched_task_item_last_error_timestamp_seconds: prometheus_client.Gauge | None = None
harness_sched_task_item_last_run_timestamp_seconds: prometheus_client.Gauge | None = None
harness_sched_task_item_last_success_timestamp_seconds: prometheus_client.Gauge | None = None
harness_sched_task_items_registered: prometheus_client.Gauge | None = None
harness_sched_task_lag_seconds: prometheus_client.Histogram | None = None
harness_sched_task_parse_errors_total: prometheus_client.Counter | None = None
harness_sched_task_reloads_total: prometheus_client.Counter | None = None
harness_sched_task_running_items: prometheus_client.Gauge | None = None
harness_sched_task_runs_total: prometheus_client.Counter | None = None
harness_sched_task_skips_total: prometheus_client.Counter | None = None
harness_triggers_requests_total: prometheus_client.Counter | None = None
harness_adhoc_fires_total: prometheus_client.Counter | None = None
harness_triggers_parse_errors_total: prometheus_client.Counter | None = None
harness_triggers_reloads_total: prometheus_client.Counter | None = None
harness_triggers_items_registered: prometheus_client.Gauge | None = None
harness_continuation_parse_errors_total: prometheus_client.Counter | None = None
harness_continuation_reloads_total: prometheus_client.Counter | None = None
harness_continuation_items_registered: prometheus_client.Gauge | None = None
harness_continuation_runs_total: prometheus_client.Counter | None = None
harness_continuation_fires_total: prometheus_client.Counter | None = None
harness_continuation_throttled_total: prometheus_client.Counter | None = None
harness_continuation_fanin_evictions_total: prometheus_client.Counter | None = None
harness_webhooks_delivery_total: prometheus_client.Counter | None = None
harness_webhooks_delivery_shed_total: prometheus_client.Counter | None = None
harness_webhooks_parse_errors_total: prometheus_client.Counter | None = None
harness_webhooks_reloads_total: prometheus_client.Counter | None = None
harness_webhooks_items_registered: prometheus_client.Gauge | None = None
harness_consensus_runs_total: prometheus_client.Counter | None = None
harness_consensus_backend_errors_total: prometheus_client.Counter | None = None
harness_metrics_backend_fetch_errors_total: prometheus_client.Counter | None = None
harness_backend_proxy_fetch_errors_total: prometheus_client.Counter | None = None
harness_background_tasks: prometheus_client.Gauge | None = None
harness_background_tasks_shed_total: prometheus_client.Counter | None = None
harness_background_tasks_timeout_total: prometheus_client.Counter | None = None
harness_backend_reachable: prometheus_client.Gauge | None = None
harness_a2a_backend_requests_total: prometheus_client.Counter | None = None
harness_a2a_backend_request_duration_seconds: prometheus_client.Histogram | None = None
harness_a2a_backend_circuit_state: prometheus_client.Gauge | None = None
harness_a2a_backend_circuit_transitions_total: prometheus_client.Counter | None = None
harness_backends_reload_errors_total: prometheus_client.Counter | None = None
harness_backends_config_stale: prometheus_client.Gauge | None = None
harness_task_store_errors_total: prometheus_client.Counter | None = None


if _enabled:
    harness_a2a_last_request_timestamp_seconds = prometheus_client.Gauge(
        "harness_a2a_last_request_timestamp_seconds",
        "Unix epoch of the most recent A2A request received.",
    )
    harness_a2a_request_duration_seconds = prometheus_client.Histogram(
        "harness_a2a_request_duration_seconds",
        "Wall-clock duration of each A2A execute() call.",
    )
    harness_a2a_requests_total = prometheus_client.Counter(
        "harness_a2a_requests_total",
        "Total A2A HTTP requests by outcome.",
        ["status"],
    )
    harness_a2a_traces_received_total = prometheus_client.Counter(
        "harness_a2a_traces_received_total",
        "Total inbound A2A requests by trace-context provenance — labelled "
        "has_inbound=true when the caller passed a valid W3C traceparent "
        "header, false when the harness minted a fresh context (#468).",
        ["has_inbound"],
    )
    harness_up = prometheus_client.Gauge("harness_up", "Agent is running", ["agent"])
    harness_active_sessions = prometheus_client.Gauge(
        "harness_active_sessions",
        "Number of active sessions tracked in the LRU cache.",
    )
    harness_job_checkpoint_stale_total = prometheus_client.Counter(
        "harness_job_checkpoint_stale_total",
        "Total stale checkpoint files found during job runner startup scan.",
    )
    harness_checkpoint_write_errors_total = prometheus_client.Counter(
        "harness_checkpoint_write_errors_total",
        "Total job checkpoint I/O failures.",
    )
    harness_job_duration_seconds = prometheus_client.Histogram(
        "harness_job_duration_seconds",
        "Wall-clock seconds per job execution.",
        ["name"],
    )
    harness_job_error_duration_seconds = prometheus_client.Histogram(
        "harness_job_error_duration_seconds",
        "Wall-clock seconds for jobs that end in error.",
        ["name"],
    )
    harness_job_lag_seconds = prometheus_client.Histogram(
        "harness_job_lag_seconds",
        "Delay between scheduled and actual job execution start.",
    )
    harness_job_parse_errors_total = prometheus_client.Counter(
        "harness_job_parse_errors_total",
        "Total job file parse failures.",
    )
    harness_job_item_last_error_timestamp_seconds = prometheus_client.Gauge(
        "harness_job_item_last_error_timestamp_seconds",
        "Unix epoch of each job's last failed run.",
        ["name"],
    )
    harness_job_item_last_run_timestamp_seconds = prometheus_client.Gauge(
        "harness_job_item_last_run_timestamp_seconds",
        "Unix epoch of each job's most recent execution, regardless of outcome.",
        ["name"],
    )
    harness_job_item_last_success_timestamp_seconds = prometheus_client.Gauge(
        "harness_job_item_last_success_timestamp_seconds",
        "Unix epoch of each job's last successful run.",
        ["name"],
    )
    harness_job_items_registered = prometheus_client.Gauge(
        "harness_job_items_registered",
        "Number of currently registered jobs.",
    )
    harness_job_reloads_total = prometheus_client.Counter(
        "harness_job_reloads_total",
        "Total job file-change reload events.",
    )
    harness_job_running_items = prometheus_client.Gauge(
        "harness_job_running_items",
        "Number of jobs currently executing.",
    )
    harness_job_runs_total = prometheus_client.Counter(
        "harness_job_runs_total",
        "Total job executions by name and outcome.",
        ["name", "status"],
    )
    harness_job_skips_total = prometheus_client.Counter(
        "harness_job_skips_total",
        "Total job skips due to previous run still in progress.",
        ["name"],
    )
    harness_bus_consumer_idle_seconds = prometheus_client.Histogram(
        "harness_bus_consumer_idle_seconds",
        "Idle time between consecutive bus worker processing cycles.",
    )
    harness_bus_dedup_total = prometheus_client.Counter(
        "harness_bus_dedup_total",
        "Total messages dropped by try_send() due to a pending message of the same kind.",
        ["kind"],
    )
    harness_bus_error_processing_duration_seconds = prometheus_client.Histogram(
        "harness_bus_error_processing_duration_seconds",
        "Wall-clock seconds for bus messages that end in error.",
        ["kind"],
    )
    harness_bus_errors_total = prometheus_client.Counter(
        "harness_bus_errors_total",
        "Total unhandled errors in the bus worker.",
    )
    harness_bus_last_processed_timestamp_seconds = prometheus_client.Gauge(
        "harness_bus_last_processed_timestamp_seconds",
        "Unix epoch of the most recent message processed by the bus worker.",
    )
    harness_bus_processing_duration_seconds = prometheus_client.Histogram(
        "harness_bus_processing_duration_seconds",
        "End-to-end processing time for each bus message.",
        ["kind"],
    )
    harness_info = prometheus_client.Info(
        "agent",
        "Static agent metadata.",
    )
    harness_bus_messages_total = prometheus_client.Counter(
        "harness_bus_messages_total",
        "Total messages processed through the message bus.",
        ["kind"],
    )
    harness_bus_pending_kinds = prometheus_client.Gauge(
        "harness_bus_pending_kinds",
        "Number of distinct message kinds currently queued in the bus.",
    )
    harness_bus_queue_depth = prometheus_client.Gauge(
        "harness_bus_queue_depth",
        "Current depth of the message bus queue.",
    )
    harness_bus_wait_seconds = prometheus_client.Histogram(
        "harness_bus_wait_seconds",
        "Seconds a message waited in the bus queue before processing.",
        ["kind"],
    )
    harness_concurrent_queries = prometheus_client.Gauge(
        "harness_concurrent_queries",
        "Number of run() calls currently in flight.",
    )
    harness_empty_responses_total = prometheus_client.Counter(
        "harness_empty_responses_total",
        "Total tasks that produced no text output.",
    )
    harness_event_loop_lag_seconds = prometheus_client.Histogram(
        "harness_event_loop_lag_seconds",
        "Excess delay beyond expected sleep duration, measuring asyncio event loop congestion.",
    )
    harness_file_watcher_restarts_total = prometheus_client.Counter(
        "harness_file_watcher_restarts_total",
        "Total file watcher restart events due to missing or deleted directory.",
        ["watcher"],
    )
    harness_heartbeat_duration_seconds = prometheus_client.Histogram(
        "harness_heartbeat_duration_seconds",
        "Wall-clock seconds from heartbeat firing to response received.",
    )
    harness_heartbeat_error_duration_seconds = prometheus_client.Histogram(
        "harness_heartbeat_error_duration_seconds",
        "Wall-clock seconds for heartbeats that end in error.",
    )
    harness_heartbeat_lag_seconds = prometheus_client.Histogram(
        "harness_heartbeat_lag_seconds",
        "Delay between scheduled and actual heartbeat execution start.",
    )
    harness_heartbeat_last_error_timestamp_seconds = prometheus_client.Gauge(
        "harness_heartbeat_last_error_timestamp_seconds",
        "Unix epoch of the most recent failed heartbeat.",
    )
    harness_heartbeat_last_run_timestamp_seconds = prometheus_client.Gauge(
        "harness_heartbeat_last_run_timestamp_seconds",
        "Unix epoch of the most recent heartbeat execution, regardless of outcome.",
    )
    harness_heartbeat_last_success_timestamp_seconds = prometheus_client.Gauge(
        "harness_heartbeat_last_success_timestamp_seconds",
        "Unix epoch of the most recent successful heartbeat.",
    )
    harness_heartbeat_load_errors_total = prometheus_client.Counter(
        "harness_heartbeat_load_errors_total",
        "Total heartbeat config load/parse failures.",
    )
    harness_heartbeat_runs_total = prometheus_client.Counter(
        "harness_heartbeat_runs_total",
        "Total heartbeat executions by outcome.",
        ["status"],
    )
    harness_heartbeat_reloads_total = prometheus_client.Counter(
        "harness_heartbeat_reloads_total",
        "Total HEARTBEAT.md file-change reload events.",
    )
    harness_health_checks_total = prometheus_client.Counter(
        "harness_health_checks_total",
        "Total HTTP health endpoint hits by probe type.",
        ["probe"],
    )
    harness_heartbeat_skips_total = prometheus_client.Counter(
        "harness_heartbeat_skips_total",
        "Total heartbeat skips due to previous heartbeat still pending.",
    )
    harness_log_bytes_total = prometheus_client.Counter(
        "harness_log_bytes_total",
        "Total bytes written by the logging subsystem.",
        ["logger"],
    )
    harness_log_entries_total = prometheus_client.Counter(
        "harness_log_entries_total",
        "Total log entries written by logger type.",
        ["logger"],
    )
    harness_log_write_errors_total = prometheus_client.Counter(
        "harness_log_write_errors_total",
        "Total I/O failures in the conversation/trace logging subsystem.",
    )
    harness_lru_cache_utilization_percent = prometheus_client.Gauge(
        "harness_lru_cache_utilization_percent",
        "LRU session cache utilization as a percentage of MAX_SESSIONS.",
    )
    harness_model_requests_total = prometheus_client.Counter(
        "harness_model_requests_total",
        "Total requests per resolved model.",
        ["model"],
    )
    harness_prompt_length_bytes = prometheus_client.Histogram(
        "harness_prompt_length_bytes",
        "Byte length of incoming prompts passed to run().",
    )
    harness_running_tasks = prometheus_client.Gauge(
        "harness_running_tasks",
        "Number of currently in-progress tasks.",
    )
    harness_response_length_bytes = prometheus_client.Histogram(
        "harness_response_length_bytes",
        "Byte length of responses returned by run().",
    )
    harness_startup_duration_seconds = prometheus_client.Gauge(
        "harness_startup_duration_seconds",
        "Time from process start to ready state in seconds.",
    )
    harness_session_age_seconds = prometheus_client.Histogram(
        "harness_session_age_seconds",
        "Seconds since last use when a session is evicted from the LRU cache.",
        buckets=(60, 300, 900, 1800, 3600, 7200, 14400, 28800, 86400),
    )
    harness_session_idle_seconds = prometheus_client.Histogram(
        "harness_session_idle_seconds",
        "Seconds a session was idle before being resumed.",
        buckets=(60, 300, 900, 1800, 3600, 7200, 14400, 28800, 86400),
    )
    harness_session_evictions_total = prometheus_client.Counter(
        "harness_session_evictions_total",
        "Total session evictions due to LRU cap.",
    )
    harness_session_starts_total = prometheus_client.Counter(
        "harness_session_starts_total",
        "Total session starts by type.",
        ["type"],
    )
    harness_task_cancellations_total = prometheus_client.Counter(
        "harness_task_cancellations_total",
        "Total task cancellation requests.",
    )
    harness_task_duration_seconds = prometheus_client.Histogram(
        "harness_task_duration_seconds",
        "Duration of agent tasks in seconds.",
    )
    harness_task_error_duration_seconds = prometheus_client.Histogram(
        "harness_task_error_duration_seconds",
        "Wall-clock seconds for tasks that end in error or timeout.",
    )
    harness_task_last_error_timestamp_seconds = prometheus_client.Gauge(
        "harness_task_last_error_timestamp_seconds",
        "Unix epoch of the most recent failed task execution.",
    )
    harness_task_last_success_timestamp_seconds = prometheus_client.Gauge(
        "harness_task_last_success_timestamp_seconds",
        "Unix epoch of the most recent successful task execution.",
    )
    harness_task_timeout_headroom_seconds = prometheus_client.Histogram(
        "harness_task_timeout_headroom_seconds",
        "Remaining timeout budget when a task completes successfully.",
    )
    harness_uptime_seconds = prometheus_client.Gauge(
        "harness_uptime_seconds",
        "Agent uptime in seconds, computed on each Prometheus scrape.",
    )
    harness_task_restarts_total = prometheus_client.Counter(
        "harness_task_restarts_total",
        "Total worker restarts by the _guarded() loop after an unexpected exception.",
        ["task"],
    )
    harness_tasks_total = prometheus_client.Counter(
        "harness_tasks_total",
        "Total agent tasks processed by outcome.",
        ["status"],
    )
    harness_watcher_events_total = prometheus_client.Counter(
        "harness_watcher_events_total",
        "Total raw file-system change events detected by each watcher.",
        ["watcher"],
    )
    harness_sched_task_checkpoint_stale_total = prometheus_client.Counter(
        "harness_sched_task_checkpoint_stale_total",
        "Total stale checkpoint files found during task runner startup scan.",
    )
    harness_sched_task_duration_seconds = prometheus_client.Histogram(
        "harness_sched_task_duration_seconds",
        "Wall-clock seconds per scheduled task execution.",
        ["name"],
    )
    harness_sched_task_error_duration_seconds = prometheus_client.Histogram(
        "harness_sched_task_error_duration_seconds",
        "Wall-clock seconds for scheduled tasks that end in error.",
        ["name"],
    )
    harness_sched_task_item_last_error_timestamp_seconds = prometheus_client.Gauge(
        "harness_sched_task_item_last_error_timestamp_seconds",
        "Unix epoch of each scheduled task's last failed run.",
        ["name"],
    )
    harness_sched_task_item_last_run_timestamp_seconds = prometheus_client.Gauge(
        "harness_sched_task_item_last_run_timestamp_seconds",
        "Unix epoch of each scheduled task's most recent execution, regardless of outcome.",
        ["name"],
    )
    harness_sched_task_item_last_success_timestamp_seconds = prometheus_client.Gauge(
        "harness_sched_task_item_last_success_timestamp_seconds",
        "Unix epoch of each scheduled task's last successful run.",
        ["name"],
    )
    harness_sched_task_items_registered = prometheus_client.Gauge(
        "harness_sched_task_items_registered",
        "Number of currently registered scheduled tasks.",
    )
    harness_sched_task_lag_seconds = prometheus_client.Histogram(
        "harness_sched_task_lag_seconds",
        "Delay between scheduled window open and actual task execution start.",
    )
    harness_sched_task_parse_errors_total = prometheus_client.Counter(
        "harness_sched_task_parse_errors_total",
        "Total scheduled task file parse failures.",
    )
    harness_sched_task_reloads_total = prometheus_client.Counter(
        "harness_sched_task_reloads_total",
        "Total scheduled task file-change reload events.",
    )
    harness_sched_task_running_items = prometheus_client.Gauge(
        "harness_sched_task_running_items",
        "Number of scheduled tasks currently executing.",
    )
    harness_sched_task_runs_total = prometheus_client.Counter(
        "harness_sched_task_runs_total",
        "Total scheduled task executions by name and outcome.",
        ["name", "status"],
    )
    harness_sched_task_skips_total = prometheus_client.Counter(
        "harness_sched_task_skips_total",
        "Total scheduled task skips due to previous run still in progress.",
        ["name"],
    )
    harness_triggers_requests_total = prometheus_client.Counter(
        "harness_triggers_requests_total",
        "Trigger endpoint HTTP requests by method and response code.",
        ["method", "code"],
    )
    harness_adhoc_fires_total = prometheus_client.Counter(
        "harness_adhoc_fires_total",
        "Ad-hoc run endpoint fire attempts for scheduled kinds (jobs, tasks, heartbeat).",
        ["kind", "name", "code"],
    )
    harness_triggers_parse_errors_total = prometheus_client.Counter(
        "harness_triggers_parse_errors_total",
        "Total trigger file parse failures.",
    )
    harness_triggers_reloads_total = prometheus_client.Counter(
        "harness_triggers_reloads_total",
        "Total trigger file-change reload events.",
    )
    harness_triggers_items_registered = prometheus_client.Gauge(
        "harness_triggers_items_registered",
        "Number of currently registered trigger endpoints.",
    )
    harness_continuation_parse_errors_total = prometheus_client.Counter(
        "harness_continuation_parse_errors_total",
        "Total continuation file parse failures.",
    )
    harness_continuation_reloads_total = prometheus_client.Counter(
        "harness_continuation_reloads_total",
        "Total continuation file-change reload events.",
    )
    harness_continuation_items_registered = prometheus_client.Gauge(
        "harness_continuation_items_registered",
        "Number of currently registered continuations.",
    )
    harness_continuation_runs_total = prometheus_client.Counter(
        "harness_continuation_runs_total",
        "Total continuation executions by name and outcome.",
        ["name", "status"],
    )
    harness_continuation_fires_total = prometheus_client.Counter(
        "harness_continuation_fires_total",
        "Total continuation firings by upstream kind.",
        ["upstream_kind"],
    )
    harness_continuation_throttled_total = prometheus_client.Counter(
        "harness_continuation_throttled_total",
        "Total continuation firings skipped due to max_concurrent_fires throttle.",
        ["name"],
    )
    harness_continuation_fanin_evictions_total = prometheus_client.Counter(
        "harness_continuation_fanin_evictions_total",
        "Total partial fan-in state entries evicted after TTL expiry.",
        ["name"],
    )
    harness_webhooks_delivery_total = prometheus_client.Counter(
        "harness_webhooks_delivery_total",
        "Outbound webhook delivery attempts by result and subscription name.",
        ["result", "subscription"],
    )
    harness_webhooks_delivery_shed_total = prometheus_client.Counter(
        "harness_webhooks_delivery_shed_total",
        "Total webhook delivery tasks shed because the concurrent delivery cap was reached.",
        ["subscription"],
    )
    harness_webhooks_parse_errors_total = prometheus_client.Counter(
        "harness_webhooks_parse_errors_total",
        "Total webhook file parse failures.",
    )
    harness_webhooks_reloads_total = prometheus_client.Counter(
        "harness_webhooks_reloads_total",
        "Total webhook file-change reload events.",
    )
    harness_backends_reload_errors_total = prometheus_client.Counter(
        "harness_backends_reload_errors_total",
        "Total backend.yaml reload attempts that failed to apply (#702). "
        "Parse failures, routing-config validation errors, or backend "
        "construction exceptions each increment this counter. Operators "
        "should alert on a non-zero rate — the harness keeps running on "
        "the last-known-good config, but updates stop flowing until the "
        "file is fixed.",
    )
    harness_backends_config_stale = prometheus_client.Gauge(
        "harness_backends_config_stale",
        "Whether the currently loaded backend.yaml is stale because the "
        "last file-change reload attempt failed (#702). 1 = stale (last "
        "reload errored), 0 = fresh (last reload succeeded or no change "
        "since startup).",
    )
    harness_task_store_errors_total = prometheus_client.Counter(
        "harness_task_store_errors_total",
        "Total SqliteTaskStore OperationalError occurrences (#704), "
        "labelled by operation (save/get/delete) and whether the call "
        "was a retry attempt. Non-zero rate signals lock contention, "
        "disk pressure, or a corrupt WAL.",
        ["op", "retry"],
    )
    harness_webhooks_items_registered = prometheus_client.Gauge(
        "harness_webhooks_items_registered",
        "Number of currently registered webhook subscriptions.",
    )
    harness_consensus_runs_total = prometheus_client.Counter(
        "harness_consensus_runs_total",
        "Total consensus-mode executions by mode and outcome.",
        ["mode", "status"],
    )
    harness_consensus_backend_errors_total = prometheus_client.Counter(
        "harness_consensus_backend_errors_total",
        "Total backend failures during consensus fan-out.",
    )
    harness_metrics_backend_fetch_errors_total = prometheus_client.Counter(
        "harness_metrics_backend_fetch_errors_total",
        "Total failures when fetching /metrics from a backend during aggregation.",
        ["backend"],
    )
    harness_backend_proxy_fetch_errors_total = prometheus_client.Counter(
        "harness_backend_proxy_fetch_errors_total",
        "Total failures when fetching /conversations or /trace from a backend "
        "during proxy aggregation (#579). endpoint=\"conversations\"|\"trace\".",
        ["backend", "endpoint"],
    )
    harness_background_tasks = prometheus_client.Gauge(
        "harness_background_tasks",
        "Number of background asyncio tasks currently in flight (webhooks, continuations, etc.).",
    )
    harness_background_tasks_shed_total = prometheus_client.Counter(
        "harness_background_tasks_shed_total",
        "Total background tasks shed because the executor's in-flight cap was reached.",
        ["source"],
    )
    harness_background_tasks_timeout_total = prometheus_client.Counter(
        "harness_background_tasks_timeout_total",
        "Total background tasks cancelled because they exceeded ON_PROMPT_COMPLETED_TIMEOUT.",
        ["source"],
    )
    harness_backend_reachable = prometheus_client.Gauge(
        "harness_backend_reachable",
        "Whether a configured backend responded OK on its last /health probe "
        "from the harness health_ready sweep (#619). 1=reachable, 0=unreachable.",
        ["backend"],
    )
    harness_a2a_backend_requests_total = prometheus_client.Counter(
        "harness_a2a_backend_requests_total",
        "Total outbound A2A requests issued by the harness to configured "
        "backends, bucketed by coarse result (#622). Result is one of "
        "ok | error_status | error_connection | error_timeout — raw HTTP "
        "status codes are intentionally NOT in the label set to bound "
        "cardinality.",
        ["backend", "result"],
    )
    harness_a2a_backend_request_duration_seconds = prometheus_client.Histogram(
        "harness_a2a_backend_request_duration_seconds",
        "Wall-clock seconds for outbound A2A requests from the harness to "
        "each configured backend (#622).",
        ["backend"],
    )
    harness_a2a_backend_circuit_state = prometheus_client.Gauge(
        "harness_a2a_backend_circuit_state",
        "Current circuit-breaker state for each configured A2A backend "
        "(#609). Exactly one state label per backend reports 1; the others "
        "report 0. state is one of closed | open | half_open.",
        ["backend", "state"],
    )
    harness_a2a_backend_circuit_transitions_total = prometheus_client.Counter(
        "harness_a2a_backend_circuit_transitions_total",
        "Total circuit-breaker state transitions per backend (#609). "
        "Labels `from` and `to` identify the transition; both take values "
        "closed | open | half_open.",
        ["backend", "from", "to"],
    )

