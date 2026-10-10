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
The full 350,000-recipient run remains the acceptance proof.
