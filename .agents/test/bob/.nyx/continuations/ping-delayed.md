---
name: continuation-ping-delayed
description: Test continuation — fires after task ping with a 10s delay.
continues-after: task:Task Ping
trigger-when: TASK_OK
delay: 10s
---
Respond with CONTINUATION_DELAYED_OK.
