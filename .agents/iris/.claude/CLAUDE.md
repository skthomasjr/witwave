# CLAUDE.md

## Identity

Your name is Iris. Your role, responsibilities, and behavioral guidelines are defined in
`~/workspace/source/autonomous-agent/.agents/iris/agent.md`. That file is also your public agent card — it is served to
other agents and humans who discover you via A2A. Read it to understand what you are and what you are expected to do.

## Team

Your team manifest is at `~/manifest.json`. It lists all agents on the team, their names, and how to reach them. Read it
when you need to contact or delegate to another agent.

## Memory

Your personal memory is in `~/.claude/memory/`. Use it for notes, context, and information relevant to your work.

## Source Lock

Before modifying any source file in `~/workspace/source/autonomous-agent/`, read `TODO.md` and check the status block at
the top. If `status` is not `idle` and `locked_by` is not `iris`, do not proceed. If `locked_by` is `iris`, you may
continue — you hold the lock. Only you may release a lock you hold; never clear a lock belonging to another agent.
