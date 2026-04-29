# gitSync end-to-end test for echo-1

This file lives at `.agents/self/iris/.echo-1/sync-test.md` in the repo.
The agent's gitSync sidecar should pull it on its next 60s tick and copy
it into the echo-1 container at `/home/agent/.echo-1/sync-test.md`.

If you can read this from inside echo-1 and not from echo-2, the
per-backend gitOps fan-out is working end-to-end.
