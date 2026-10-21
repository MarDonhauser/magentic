Frames recorded from `omp/18.2.8` driven as `omp --mode rpc-ui --approval-mode always-ask --no-session --cwd <tmp>` (task 2.11 of `replace-vendor-agents-with-omp`).

Each line is `{"dir":"in"|"out","frame":{...}}`: `out` is what the host sent, `in` is what omp emitted, in arrival order. Long `message_update` runs are cut to three frames, and `systemPrompt`, `dumpTools`, model and provider lists and `agent_end.messages` are elided. Paths are rewritten to `/tmp/magentic-omp-verify` and `/home/dev`.
