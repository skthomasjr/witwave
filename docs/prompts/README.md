# Prompts

Prompts are markdown files with YAML frontmatter that define what gets dispatched to a backend LLM. Each prompt type
differs only in how and when it is triggered — the body is always the prompt text sent to the agent.

## Prompt Types

| Type                              | File location             | Trigger                                  |
| --------------------------------- | ------------------------- | ---------------------------------------- |
| [Heartbeat](heartbeat.md)         | `.nyx/HEARTBEAT.md`       | Cron schedule (single file)              |
| [Jobs](jobs.md)                   | `.nyx/jobs/*.md`          | Cron schedule (one file per job)         |
| [Tasks](tasks.md)                 | `.nyx/tasks/*.md`         | Calendar window (days, time, date range) |
| [Triggers](triggers.md)           | `.nyx/triggers/*.md`      | Inbound HTTP POST                        |
| [Continuations](continuations.md) | `.nyx/continuations/*.md` | Upstream prompt completion               |
| [Webhooks](webhooks.md)           | `.nyx/webhooks/*.md`      | Outbound HTTP after any prompt completes |

All prompt files support `model:` and `agent:` frontmatter fields to override the default backend routing on a
per-prompt basis.
