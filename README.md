# log-progress-indicator

`lpi` estimates the completion percentage, units of work done, and ETA of a
long-running task (a build, a test suite, a batch job) by fuzzy-matching its
partial log output against reference logs recorded from previous completed
runs. Log lines are normalized into stable templates (timestamps, counters,
hashes, and addresses are stripped), so matching is robust to run-to-run
variation and to out-of-order interleaving from parallel jobs.

Work in progress -- the CLI surface is still being built out. See
`docs/DESIGN.md` for the design.
