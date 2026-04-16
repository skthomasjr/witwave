---
name: continuation-fanin-test
description: Fan-in smoke test — fires only after both fanin-a and fanin-b jobs have completed in the same session.
continues-after:
  - job:fanin-a
  - job:fanin-b
---
Respond with FANIN_OK.
