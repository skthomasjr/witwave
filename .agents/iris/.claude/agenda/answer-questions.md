---
name: Answer Questions
description: Polls for open GitHub Issues with type/question, claims one, researches and answers it thoroughly.
schedule: "*/5 * * * *"
enabled: true
---

Poll for open unanswered questions in GitHub Issues and answer them. Also recover any questions that have been stuck
in-progress for too long.

Steps:

1. **Recover stale in-progress questions** — use
   `gh issue list --state open --label "type/question" --json number,updatedAt` to list all open questions. For each,
   fetch the full issue body using `gh issue view <number> --json body --jq '.body'` and check the `Claimed by:` field.
   For any issue where `Claimed by:` is not `none` and whose `updatedAt` is more than 1 hour ago, reclaim it by posting
   a comment `[iris] Reclaiming stale question — resuming from in-progress` and updating the body to set
   `Claimed by: iris`. Then proceed to answer it starting from step 3. If no stale in-progress questions are found,
   continue to step 2.

2. **Find next unclaimed question** — from the same open `type/question` issues fetched in step 1, take the first issue
   where `Claimed by:` is `none`. If no unclaimed open issue exists and no stale issues were found in step 1, stop —
   there is nothing to do this run.

3. Claim the issue using `/github-issue claim <number> iris`.

4. Read the full issue body to extract the question text.

5. Determine whether the question is relevant to this project — its codebase, configuration, agents, or behavior. If it
   clearly is not, post a polite comment explaining that the question falls outside the scope of this repository and
   close the issue using `/github-issue close <number> "Closed — question is outside the scope of this project"`. Stop
   here for this run.

6. Research the question thoroughly. The goal is a complete, first-principles understanding — not a keyword search. Do
   not skim. Leave no relevant source unread.

   - **Repo files** — read all source code, documentation, configuration, Dockerfile, agent cards, agendas, and skills
     under `<repo-root>`. Understand the full architecture: how components are structured, how they interact, how data
     flows through the system, and why key design decisions were made. Answer the question from this deep understanding,
     not from surface-level pattern matching.
   - **Git history** — use `git log`, `git log --follow`, and `git blame` in `<repo-root>` to understand why changes
     were made and how the code evolved. Commit messages often contain context that is not visible in the code itself.
   - **GitHub Issues** — run multiple targeted keyword searches using `gh search issues <keywords>`. Draw keywords from
     the question itself and related concepts. Cast a wide net — prior questions, tasks, and comments in both open and
     closed issues may contain directly relevant context or prior answers.
   - **External sources** — use WebSearch and WebFetch if the question requires knowledge beyond the repo: SDK behavior,
     protocol specifications, library documentation, or anything else not fully answered by internal sources alone.

7. Compose a thorough, precise answer. Cite specific files and line numbers where relevant. If any aspect of the
   question cannot be answered with confidence after research, say so explicitly rather than speculating.

8. Post the answer as a comment using `/github-issue comment <number> <answer>`.

9. Close the issue using `/github-issue close <number> "Answered by iris"`.

10. Check for additional open unclaimed questions by repeating step 2. If another unclaimed `type/question` issue
    exists, continue from step 3. Repeat until no unclaimed questions remain.

11. Reflect on this run. If any difficulties were encountered — tools that didn't behave as expected, steps that were
    ambiguous, sources that were missing or hard to navigate, or anything that made answering questions harder than it
    should be — and a resolution is evident, create a GitHub Issue using the `/github-issue create task` skill to track
    the improvement using `/github-issue create task status/approved`. If the run was smooth, do nothing.
