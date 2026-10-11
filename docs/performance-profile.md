# Sender profiling and pacing

The historical runs below used WAL `synchronous=NORMAL`. They prove sender
behavior and process interruption recovery for that configuration. They do not
prove host or power interruption durability. The current configuration and
comparable measurements are at the end of this document.

The sender keeps the SES account quota as its token refill rate. Its burst is
`min(max(1, floor(rate)), max(5, ceil(rate * 0.020)))`, allowing a bounded 20ms
catch-up after a scheduler delay, with the existing five-token minimum. At 175 messages/s the allowance remains five
tokens; at 1,000 messages/s it is twenty. Reservations remain serialized so
workers cannot build up overdue reservations and release them together.
`TestQuotaBurstPreservesRefillEnvelope` checks irregular polling against the
token bucket envelope across fractional, low, and high quotas. This allowance
does not promise that a wall-clock completion bucket stays below the quota:
network latency and jitter can move completions into adjacent seconds.

The request-span metrics use an immutable monotonic origin. A wall-clock
adjustment cannot change the rate denominator. The load test already measures
the complete campaign with Go's monotonic `time.Since`, including audience
snapshot preparation and final result persistence.

Optional profiles can be captured without altering the reported campaign span:

```sh
go build -o /tmp/postbox-profile-load ./cmd/loadtest
/tmp/postbox-profile-load -recipients 20000 -rate 1000 \
  -cpu-profile /tmp/postbox-cpu.pprof \
  -heap-profile /tmp/postbox-heap.pprof \
  -output /tmp/postbox-profile.json
go tool pprof -top /tmp/postbox-profile-load /tmp/postbox-cpu.pprof
go tool pprof -top -sample_index=alloc_space /tmp/postbox-profile-load /tmp/postbox-heap.pprof
```

CPU profiling covers `RunCampaign`; the optional heap profile is collected after
completion and GC. Peak heap still comes from periodic samples during sending.
Profile finalization and heap GC happen after measurement stops. The fixture is
an HTTP SES replacement in the same process, so these profiles include both the
real SDK sender and fake server.

On the development Mac, a 20,000-recipient CPU profile with 100KB HTML and
100ms plus 50ms jitter used about 2.5 CPU cores. Approximately 72% of sampled CPU
was runtime scheduling and syscalls. Fake HTTP request decoding accounted for
19.1% cumulative CPU. Of 52.6GB cumulative allocations, 46.1% originated in the
fake HTTP handler, 34.2% in the real AWS SDK adapter, and 17.5% in MIME rendering.
The completed live heap was 13.1MB, while sampled sending peak heap was 274.7MB.
These results show substantial transient allocation pressure and fixture
overhead. The fake additionally retains a recipient map for duplicate checks,
which grows with the audience. Production sends do not retain that fixture map
or decode their own outgoing HTTP requests. A completed heap profile cannot
establish a production peak-memory bound.

The unprofiled 50,000-recipient validation at 1,000/s was run with the same HTML,
latency and jitter while the independent 175/s proof continued. Results:

| Metric | Five-token baseline | Twenty-token scheduler allowance |
| --- | ---: | ---: |
| Complete campaign rate | 961.48/s | 988.67/s |
| Duration | 52.003s | 50.573s |
| Request-span rate | 971.77/s | 999.79/s |
| CPU seconds | 131.84 | 129.62 |
| Peak heap | 415.57MB | 267.14MB |
| Peak RSS | 557.69MB | 422.13MB |
| Request p99 | 185ms | 152ms |
| Writer p99 | 107ms | 17ms |
| Accepted, sent | 50,000 | 50,000 |
| Duplicates | 0 | 0 |

These sequential runs are not an isolated benchmark, so the exact improvement
and memory difference include machine contention and GC variation. The modified
run passed the complete-campaign 2% tolerance without raising the account quota.
The full 350,000-recipient ledger runs are the acceptance proof; see the final run below.

## Durable dispatch ledger regression

The first ledger smoke slowed as the campaign grew, with completion writer
p99 around 1.1 seconds and no requests in flight during stalled batches.
The exact completion UPDATE plan on live ledgers of 10,000 and 35,646 rows
used only the campaign prefix of the WITHOUT ROWID primary key. This made
every completion revisit the campaign's dispatches, despite an existing
owner index. A 10,000-recipient CPU profile attributed 15.12% of total sampled
CPU to `finishDispatchTx` and 22.65% to SQLite's VM execution overall.

The completion UPDATE now names `campaign_dispatch_owner`, constraining
campaign, owner subscriber and attempt. Its durable generation comparison
and immediate transaction remain in place. The production-source plan test
requires the full owner and journal keys both before and after ANALYZE.

The subsequent 50,000-recipient smoke at 1,000/s completed in 50.897 seconds,
achieving 982.37/s over the complete campaign and 994.50/s over the request
span. Accepted and sent counts were both 50,000, duplicates were zero, and
failed and unknown counts were zero. Request p99 was 152ms and writer p99
was 257ms. Sampled peak heap was 367.68MB, RSS was 511.95MB, and CPU time was
144.75 seconds. This run overlapped other validation work, so its resource
figures include contention. It passed the 2% complete-campaign tolerance;
the full ledger-enabled 350,000-recipient runs are the acceptance proof.

## Full 350,000-recipient ledger proof

| Configured rate | Run context | Complete rate | Request rate | Duration | Request p50/p99 | Writer p99 | Peak heap | Peak RSS | CPU | Accepted / sent | Duplicates / failed / unknown | Max accepted in one second |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1,000/s | Isolated | 989.62/s | 997.63/s | 353.670s | 127/152ms | 62ms | 387.5MB | 808.8MB | 1,048.0s (296.3%) | 350,000 / 350,000 | 0 / 0 / 0 | 1,232 |
| 175/s | Overlapped UI builds, CLI cross-builds, and full test runs | 173.18/s | 173.42/s | 2,021.036s | 128/170ms | 56ms | 204.0MB | 422.9MB | 1,618.1s (80.1%) | 350,000 / 350,000 | 0 / 0 / 0 | 191 |

Both full campaigns completed within the 2% rate tolerance with the durable
dispatch ledger enabled. Resource figures include both the client and in-process
fake SES fixture. The 175/s run overlapped UI builds, CLI cross-builds and full
test runs, so its results also reflect concurrent machine load.

The isolated 1,000/s pass does not establish an unconditional production
throughput or memory bound. A separate contended full run achieved 978.80/s,
2.12% below target and just outside the tolerance. The isolated pass does not
prove contention was the only cause of that difference.

The busiest wall-clock seconds contained 1,232 accepted completions at 1,000/s
and 191 at 175/s. These are completion buckets affected by request latency and
jitter, not dispatch admission rates. The token bucket bounds dispatch starts
by its refill rate and burst allowance: at most a twenty-token catch-up burst
at 1,000/s, or five at 175/s. It does not promise each observed completion
bucket stays at or below the configured quota.

## Reservation ordering, sync choice and current throughput

SQLite connections use WAL `synchronous=NORMAL` and `fullfsync=OFF` through
URI connection initialization. Both the single writer and every replacement
physical connection receive those settings before any transaction begins.
Settings are never changed inside a transaction. NORMAL preserves committed
transactions across an application-process crash, but a host crash or power
failure can lose recent committed transactions. A lost dispatch reservation
could permit a duplicate send after restart. This setting prioritizes delivery
throughput over host-crash durability. `fullfsync` requests an Apple-specific stronger
disk-cache flush and does not add a ZFS feature on Linux. See
[SQLite's synchronous documentation](https://www.sqlite.org/pragma.html#pragma_synchronous)
and [fullfsync documentation](https://www.sqlite.org/pragma.html#pragma_fullfsync).

Campaign `beginAttempt` commits the attempt journal and dispatch ledger before
calling `Sender.Send`. Transactional and notification jobs commit the running
lease before invoking the handler, which may contact SES. Completion follows
external acceptance in a separate transaction. An interrupted campaign attempt
stays started or becomes unknown; interrupted mail jobs become failed with an
unknown-delivery explanation. Neither can be automatically dispatched again.
Existing campaign retry guards and the email job terminal-state rules remain
in force. Do not manually reset unknown reservations or replay their payloads:
SES may already have accepted them. A new request after an administrative reset
cannot establish non-delivery.

Tests verify NORMAL and fullfsync OFF on live pooled connections and forced writer
replacements, independently observable committed campaign journals at fake send
entry, and no send after an injected reservation-write failure. Separate
campaign and transactional child-process tests interrupt execution after fake
acceptance and before completion, reopen the database, and verify recovery does
not resend. Those tests simulate a process crash, not a host crash or loss of
power. They cannot establish host or power-loss durability. External deletion,
restoration of an older database and bypassing reservation APIs can also lose
the at-most-once guarantee. No real mail was sent in these tests or measurements.

The load fixture now accepts `-background-write-interval`; a 10ms setting-update
worker competes for the same serialized writer with attempt reservations,
claim batches and completion batches. The reported background write count is
successful commits, not the requested frequency, since the writer can be busy.
Reports include effective `sqlite_synchronous` and `sqlite_fullfsync` values.
Reproduce a representative run with:

```sh
go build -o /tmp/postbox-load ./cmd/loadtest
/tmp/postbox-load -recipients 10000 -rate 300 \
  -background-write-interval 10ms \
  -output /tmp/postbox-normal-300.json
```

These runs use 100KB HTML, 100ms base fake HTTP SES latency plus 0-50ms
jitter, real AWS SDK serialization, and concurrent setting commits on a
development Mac. They include snapshot preparation and final persistence. They
do not predict throughput on Linux ZFS, which needs its own measurement.

| Configuration | Recipients | Configured rate | Complete rate | Duration | Writer p99 | Background commits | Accepted / sent | Duplicate / failed / unknown |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| FULL, fullfsync ON | 2,000 | 175/s | 50.39/s | 39.692s | 5,814ms | 64 | 2,000 / 2,000 | 0 / 0 / 0 |
| FULL, fullfsync ON | 2,000 | 1,000/s | 54.73/s | 36.541s | 7,090ms | 35 | 2,000 / 2,000 | 0 / 0 / 0 |
| FULL, fullfsync OFF | 10,000 | 300/s | 297.79/s | 33.580s | 10ms | 3,326 | 10,000 / 10,000 | 0 / 0 / 0 |
| FULL, fullfsync OFF | 10,000 | 1,000/s | 977.19/s | 10.233s | 25ms | 982 | 10,000 / 10,000 | 0 / 0 / 0 |
| NORMAL, fullfsync OFF | 10,000 | 300/s | 297.84/s | 33.575s | 12ms | 3,346 | 10,000 / 10,000 | 0 / 0 / 0 |
| NORMAL, fullfsync OFF | 10,000 | 1,000/s | 965.14/s | 10.361s | 242ms | 970 | 10,000 / 10,000 | 0 / 0 / 0 |

The 300/s NORMAL run met its configured rate within 2%. The 1,000/s NORMAL
run reached 965/s over the full campaign and 989/s during requests; the total
rate includes fixed setup and final persistence. FULL without fullfsync also
cleared 250/s on this Mac. The much slower earlier FULL-only run below shows
substantial run-to-run variation, so the table is evidence for this fixture,
not a guarantee of a Linux ZFS deployment. Measure that host before capacity
planning. The table retains the results; raw synthetic run files are not kept
in the repository.

An earlier FULL-only 10,000-recipient run, before enabling fullfsync,
achieved 159.72/s at configured 1,000/s in 62.608s. Its writer p99 was 7,407ms,
background commits 59, and all 10,000 were accepted/sent with zero duplicates,
failures or unknowns. The difference from the later FULL-only run was not
isolated, so these results should not be attributed solely to the sync pragma.
