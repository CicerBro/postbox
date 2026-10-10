# Sender profiling and pacing

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

Both full campaigns completed within the 2% rate tolerance with the durable dispatch ledger enabled. Resource figures include both the client and in-process fake SES fixture. The 175/s resource and throughput figures also reflect concurrent machine load. Raw run artifacts: [1,000/s isolated](performance/1000-isolated-350000.json) and [175/s overlapped](performance/175-350000.json).

The isolated 1,000/s run is recorded in
[`performance/1000-isolated-350000.json`](performance/1000-isolated-350000.json).
It completed all 350,000 recipients in 353.670 seconds, or 989.62/s across the
complete campaign, within the 2% acceptance tolerance. The request span achieved
997.63/s. All 350,000 sends were accepted and sent; failed, unknown, and duplicate
counts were zero. Request p50/p99 were 127/152ms and writer p99 was 62ms.
Sampled peak heap was 387.5MB, peak RSS was 808.8MB, and combined process CPU
was 1,048.0 seconds (296.3% of one core). The fixture is in-process and its
resource figures include both the client and fake SES server.

The fixture observed a maximum of 1,232 accepted completions in one wall-clock
second. This does not establish a 1,232/s admission rate or mean the configured
quota was exceeded. The token bucket bounds dispatch starts by its refill rate
and burst allowance, while request latency and jitter can shift completions
across second boundaries. The configured bucket permits at most a 20-token
catch-up burst at 1,000/s (five at 175/s); it does not promise every observed
completion bucket stays at or below the quota.

The run proves this isolated 350,000-recipient case at 1,000/s with the durable
dispatch ledger enabled. It does not establish an unconditional production
throughput or memory bound. The full contended 1,000/s run achieved 978.80/s, 2.12% below target and just
outside the 2% complete-campaign tolerance. The isolated pass does not prove
that contention was its only cause. The full 175/s run is recorded in
[`performance/175-350000.json`](performance/175-350000.json). It completed in
2,021.036 seconds at 173.18/s over the full campaign, within the 2% tolerance;
the request span achieved 173.42/s. Accepted and sent counts were 350,000, with
zero duplicates, failures, or unknown outcomes. Request p50/p99 were 128/170ms
and writer p99 was 56ms. Sampled peak heap was 204.0MB, peak RSS was 422.9MB,
and combined process CPU was 1,618.1 seconds (80.1% of one core). The run
overlap included UI builds, CLI cross-builds, and full test runs, so resource
and throughput results reflect concurrent machine load. The fixture observed
191 completions in its busiest wall-clock second; as with the 1,000/s run, this
is a completion bucket affected by request latency and jitter, not the dispatch
start rate.
