# Production query plan proof

Run `go test ./internal/store ./internal/send -run TestProduction -count=1 -v`.
The verbose output is the SQLite `EXPLAIN QUERY PLAN` output for source-derived
runtime statements, including UPDATE statements, rather than copied simplified
SELECT statements. The tests add no SQL or index hints to the statements they
explain. The durable dispatch completion statement itself names its owner index
to guarantee a bounded lookup even before SQLite has collected statistics.

`internal/store/queryplan_test.go` parses production Go source and resolves the
complete string expressions and package constants used by named runtime methods.
It rejects missing, ambiguous and unresolved query expressions. For conditional
recipient reports and CSV list exports it reads the actual base, conditional and
ordering fragments from the corresponding method. Segment predicates come from
`subscribers.CompileSegment`; custom fields and their expression indexes are created
through `SaveCustomField`, with the hint format read from `SegmentCount`. The
custom count also runs the service method and requires the plan to constrain both
brand and the indexed JSON expression.

Coverage includes the actual sender claim SELECT, claim UPDATE, result UPDATE,
SES MessageID lookup, normalized subscriber email lookup, subscriber keyset page,
subscriber memberships, recipient reports with and without status, recipient CSV
export, subscriber/list CSV export, campaign/brand/link/country/hour rollups,
UPDATE RETURNING due-job claim, scheduler campaign polling, raw open/click and SES
history erasure, list/opened/clicked/inactive/status segment counts and a custom
JSON-field count.

The store fixture populates 10,000 subscribers, memberships, recipients, opens,
clicks, SES events and jobs across 40 brands, including several recipient states,
and runs ANALYZE. Rollup tables are populated as well. Every covered statement
must have a SEARCH step except the separately documented small campaigns poll.
Any SCAN of subscribers, list_subscribers, campaign_recipients, opens, clicks or
ses_events fails, including index-backed SCANs and the aliases used by the
covered runtime statements.

`internal/send/queryplan_test.go` calls the real `Engine.audienceSQL` builder and
reads the complete snapshot INSERT and consent EXISTS wrappers from production
source. It proves include-list, include-segment, exclude-list and exclude-segment
combinations on a 10,000-subscriber fixture and runs ANALYZE. Each combination
also executes the exact snapshot INSERT, checking parameter ordering and the
ON CONFLICT suffix. Large audience sources must use SEARCH, without forcing an
index. The singleton consent check must likewise avoid scanning these sources.

`TestProductionDispatchCompletionPlan` explains the exact runtime ledger
completion UPDATE on 30,000 dispatches, before and after ANALYZE. It requires
the lookup to constrain campaign, owner subscriber and attempt, and requires
the generation-fencing journal subquery to constrain the full journal key.
A SEARCH constrained only by campaign fails this proof: it still visits every
dispatch in that campaign for every completion. Without the production owner
index hint, the live 10,000- and 35,646-row smoke databases selected the
WITHOUT ROWID primary key with only `campaign_id=?`, producing increasing
completion-batch latency as the campaign grew. The existing
`campaign_dispatch_owner` index makes each completion lookup bounded while
preserving the generation check and atomic transaction.

## Limits

This is an optimizer regression proof on representative multibrand data, not an
execution-time benchmark or a proof for every possible SQLite distribution.
The small campaigns table is scanned by actual `Engine.Start` polling in this
fixture; the test records this explicitly and does not claim indexed scheduler
polling. Suppression tables have two rows and may be scanned. They are outside
the six large tables covered by the no-scan contract.

The older `store.TestHotQueryPlans` examples remain supplemental index checks.
Their copied or simplified queries are not counted as production-source proof.
