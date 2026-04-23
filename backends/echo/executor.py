"""Echo backend AgentExecutor.

The echo backend is a zero-dependency A2A backend used as the default
for ``ww agent create`` hello-world onboarding. It requires no API keys,
no external services, and no persistent state — it exists so a new user
can deploy a live agent with "access to a Kubernetes cluster and the CLI"
as the only prerequisites.

Echo also doubles as a **reference implementation** of the common A2A
backend contract. When a new backend type is added (Ollama, Mistral,
self-hosted, …), echo is the template to copy from — it demonstrates the
A2A wiring, the dedicated-port metrics listener, and the common
``backend_*`` metric baseline without the LLM-SDK coupling the real
backends carry.

On every A2A task it returns a canned response quoting the caller's
prompt and pointing at the next step (swapping in a real backend). The
response is intentionally self-documenting so the backend teaches users
what echo is for without them needing to read docs first.
"""

import time

from a2a.server.agent_execution import AgentExecutor as A2AAgentExecutor
from a2a.server.agent_execution import RequestContext
from a2a.server.events import EventQueue
from a2a.utils import new_agent_text_message

import metrics


_CANNED_RESPONSE_TEMPLATE = (
    "echo backend — no LLM configured.\n\n"
    "You said: {prompt}\n\n"
    "This agent is running the echo backend, which returns canned responses "
    "so you can deploy and exercise an agent without any API keys. To swap "
    "in a real backend (claude, codex, or gemini), see `ww agent backend set --help`."
)

_EMPTY_PROMPT_RESPONSE = (
    "echo backend — received an empty prompt. "
    "Send text and I'll echo it back."
)


class EchoAgentExecutor(A2AAgentExecutor):
    """Trivially stateless A2A executor. Every request returns canned text.

    The executor instruments ``execute()`` with the common ``backend_a2a_*``
    request-surface metrics and the ``backend_{prompt,response}_length_bytes``
    histograms. Every metric update is guarded against a disabled registry
    (``if X is not None``) so a backend with ``METRICS_ENABLED`` unset pays
    zero runtime cost.
    """

    def __init__(self, *, labels: dict[str, str]):
        """
        Args:
            labels: The ``(agent, agent_id, backend)`` label dict applied to
                every metric emission. Passed in from main.py so the
                executor doesn't hard-code identity.
        """
        super().__init__()
        self._labels = labels

    async def execute(self, context: RequestContext, event_queue: EventQueue) -> None:
        start = time.monotonic()
        status = "ok"
        try:
            prompt = (context.get_user_input() or "").strip()

            if metrics.backend_prompt_length_bytes is not None:
                metrics.backend_prompt_length_bytes.labels(**self._labels).observe(
                    len(prompt.encode("utf-8")),
                )

            if not prompt:
                status = "error"
                if metrics.backend_empty_prompts_total is not None:
                    metrics.backend_empty_prompts_total.labels(**self._labels).inc()
                text = _EMPTY_PROMPT_RESPONSE
            else:
                text = _CANNED_RESPONSE_TEMPLATE.format(prompt=prompt)

            if metrics.backend_response_length_bytes is not None:
                metrics.backend_response_length_bytes.labels(**self._labels).observe(
                    len(text.encode("utf-8")),
                )

            await event_queue.enqueue_event(new_agent_text_message(text))
        except Exception:
            status = "error"
            raise
        finally:
            duration = time.monotonic() - start
            if metrics.backend_a2a_requests_total is not None:
                metrics.backend_a2a_requests_total.labels(**self._labels, status=status).inc()
            if metrics.backend_a2a_request_duration_seconds is not None:
                metrics.backend_a2a_request_duration_seconds.labels(**self._labels).observe(duration)
            if metrics.backend_a2a_last_request_timestamp_seconds is not None:
                metrics.backend_a2a_last_request_timestamp_seconds.labels(**self._labels).set(time.time())

    async def cancel(self, context: RequestContext, event_queue: EventQueue) -> None:
        # Echo tasks complete synchronously within execute(); nothing to cancel.
        # The A2A framework still calls cancel() on explicit client cancellation,
        # so provide a no-op implementation rather than letting it raise.
        return None
