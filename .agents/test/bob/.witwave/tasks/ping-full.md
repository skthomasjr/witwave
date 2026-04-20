---
name: task-ping-full
description: Test task — exercises all task scheduler fields in combination.
start: 2026-01-01
days: "1-5"
timezone: America/Chicago
window-start: "08:00"
window-duration: 4h
loop: true
loop-gap: 30m
done-when: FULL_DONE
model: claude-haiku-4-5-20251001
enabled: true
---

Respond with FULL_DONE.
