# Implementation contracts

Module: `postbox`, Go 1.27. Single binary at `cmd/postbox`. Agents own separate packages and do not edit go.mod/go.sum. Coordinator manages dependencies and integration. All timestamps are Unix milliseconds. Use the schema in build-plan.md, extending via new migrations as needed. No external runtime services beyond AWS SES/SQS and optional Sendy source.

## Store (`internal/store`)

`type DB struct { Writer *sql.DB; Reader *sql.DB }`, `Open(path string) (*DB,error)`, `Close() error`, `Immediate(ctx context.Context, fn func(*sql.Conn) error) error`. Immediate obtains the writer connection, begins IMMEDIATE, calls fn, commits or rolls back. Embed migrations. Other packages own their SQL and access these fields. Schema additions must be coordinated with the store agent. Include settings(key TEXT PRIMARY KEY,value TEXT NOT NULL), send_attempts and campaign preparation persistence. All FK columns indexed.

## Rendering (`internal/render`)

`type Recipient struct { ID int64; Email, Name string; Fields map[string]string; Token, UnsubscribeURL, WebVersionURL string }`.
`type Options struct { CampaignID int64; Subject, HTML, Text, TrackingURL string; InlineCSS bool; Fields []string; Now time.Time }`.
`type Link struct { ID int64; URL string }`.
`type Prepared` has exported Subject/HTML/Text strings (source after preparation), Links []Link, and compiled internal chunks.
`Prepare(Options) (*Prepared,error)`, `(*Prepared).Render(Recipient) (subject,html,text string,err error)`.
`type Message struct { FromName, FromEmail, ReplyTo, ToName, ToEmail, Subject, HTML, Text, UnsubscribeURL, UnsubscribeMailto string; Date time.Time }`; `BuildMIME(Message) ([]byte,error)`. `NormalizeEmail(string) (string,error)` shared normalization.

## Sending (`internal/send`)

`type Quota struct { MaxSendRate, Max24HourSend, SentLast24Hours float64 }`.
`type Request struct { From string; To string; Raw []byte; ConfigurationSet string; BrandID, CampaignID int64 }`.
`type Sender interface { GetAccount(context.Context) (Quota,error); Send(context.Context,Request) (string,error) }`.
AWS implementation in same package, fake implementation and HTTP fake SES command for testing.
`type Config struct { MaxWorkers int; ClaimBatch int; ResultBatch int; FlushInterval, QuotaRefresh time.Duration; RecoveryPolicy string; BaseURL string; TokenSecret []byte; SoftBounceLimit int }`.
`New(db *store.DB,sender Sender,cfg Config) *Engine`, `Start(ctx context.Context) error` recovers then runs scheduled loop; `RunCampaign(ctx context.Context,id int64) error`, `Pause(ctx,id) error`, `Resume(ctx,id) error`, `Cancel(ctx,id) error`, `SetEnabled(ctx,bool) error`, `Estimate(ctx,id) (Estimate,error)`; Export estimate JSON fields and engine metrics. Persist prepared campaign source and sender envelope for web version identity. Tracking tokens via internal/tracking/token package shared by renderer/send/events.

## Events (`internal/tracking` and `internal/events`)

Token utility owned by renderer agent at `internal/tracking/token`: `Encode(secret []byte,campaignID,subscriberID int64) string`, `Decode(secret []byte,value string) (campaignID,subscriberID int64,err error)`.
Tracking/event package agent owns `internal/tracking` excluding token and `internal/events`. Expose `New(db,*Config)`, `Handler` route registration or `Register(mux)` and lifecycle methods. Communicate exact API before API agent starts. Unsubscribes must persist immediately before HTTP success. SNS signature validation fail closed. No circular send import; database used for queue skip.

## Jobs and subscribers

`internal/jobs`: durable generic handler registry, claim/lease/retry, import/webhook/retention/backup tasks. `internal/subscribers`: normalization uses render.NormalizeEmail, streamed CSV, segment rules compile safely, filterable custom field expression indexes, subscriptions/mass erase, suppression, Sendy migration in separate internal/migrate agent.

## HTTP and UI

`internal/httpapi`: standard net/http routing, auth/TOTP/CSRF, brand API keys, handlers, OpenAPI. Receives DB and engine via constructor, registers subscribers/events implementations as dependencies. `internal/web` embeds UI assets. Bun React/Tailwind v4/shadcn style/TanStack Query/CodeMirror. UI work starts after sender proof. CLI handles config flags/env, initialization, setup admin, service lifecycle, importer, loadtest.

Avoid fake success and unimplemented placeholder endpoints. Errors must be actionable and propagated. No em dashes in user replies.
