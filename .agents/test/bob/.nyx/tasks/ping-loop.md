---
name: Task Ping Loop
description: Test task — verifies looping behavior within a window. Runs Mon-Fri, loops every 10 minutes until LOOP_DONE.
days: "1-5"
window-start: "00:00"
window-duration: 1h
loop: true
loop-gap: 10m
done-when: LOOP_DONE
enabled: true
---

If you have responded to this task fewer than 3 times today, respond with LOOP_OK. Otherwise respond with LOOP_DONE.
