// Shared types for scheduler/runtime views. Shapes mirror each harness
// endpoint verbatim so re-typing is the point of friction when the
// harness contract drifts.

export interface Job {
  name: string;
  schedule: string | null;
  session_id: string | null;
  backend_id: string | null;
  model: string | null;
  consensus?: string[];
  max_tokens?: number | null;
  running: boolean;
}

export interface Task {
  name: string;
  days_expr: string;
  timezone: string;
  window_start: string;
  window_end: string;
  loop: boolean;
  session_id: string | null;
  backend_id: string | null;
  model: string | null;
  consensus?: string[];
  max_tokens?: number | null;
  start: string | null;
  end: string | null;
  running: boolean;
}

export interface Trigger {
  name: string;
  endpoint: string;
  description: string;
  session_id: string | null;
  backend_id: string | null;
  model: string | null;
  consensus?: string[];
  max_tokens?: number | null;
  running: boolean;
  enabled: boolean;
  signed: boolean;
}

export interface Webhook {
  name: string;
  url: string;
  notify_when: string;
  notify_on_kind: string[];
  notify_on_response: string[];
  description: string;
  enabled: boolean;
  retries: number;
  backend_id: string | null;
  model: string | null;
  active_deliveries: number;
  max_concurrent_deliveries: number;
}

export interface Continuation {
  name: string;
  continues_after: string | string[];
  on_success: boolean;
  on_error: boolean;
  trigger_when: string | null;
  delay: number | null;
  description: string;
  backend_id: string | null;
  model: string | null;
  consensus?: string[];
  max_tokens?: number | null;
  max_concurrent_fires: number;
  active_fires: number;
}

export interface Heartbeat {
  enabled: boolean;
  schedule: string | null;
  model: string | null;
  backend_id: string | null;
  consensus: string[];
  max_tokens: number | null;
}
