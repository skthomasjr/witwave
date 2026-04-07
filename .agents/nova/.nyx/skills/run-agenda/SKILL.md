---
name: run-agenda
description: Run a named agenda item on demand
argument-hint: "<agenda-name>"
---

Run the named agenda item from `~/.nyx/agenda/`.

**Arguments:** $ARGUMENTS

Steps:

1. Parse the agenda name from the arguments (the full argument is the agenda name, without the `.md` extension).
2. Look for the file at `~/.nyx/agenda/<agenda-name>.md`. If it does not exist, report the error clearly and stop.
3. Read the file. Strip the frontmatter (everything between the opening and closing `---` lines) — the remaining content
   is the prompt.
4. Execute the prompt exactly as the agenda runner would — follow every instruction in it as written.
