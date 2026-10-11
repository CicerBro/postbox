# Postbox handbook

The manual now lives in six chapters, also available after signing in at `/docs`. Each chapter follows a complete task, with detailed references kept alongside the relevant workflow.

| Chapter | Covers |
| --- | --- |
| [Welcome](docs/handbook/welcome.md) | Overview and navigation |
| [Getting started](docs/handbook/getting-started.md) | First run, AWS connection, sender domains and SES events |
| [Sending newsletters](docs/handbook/sending-newsletters.md) | Audiences, signup, consent, editing, scheduling and delivery |
| [Running Postbox](docs/handbook/running-postbox.md) | Docker/systemd, HTTPS, access, backups, monitoring and configuration |
| [Moving from Sendy](docs/handbook/moving-from-sendy.md) | Source preparation, preview, import, status review and retries |
| [Development](docs/handbook/development.md) | Build and test workflow, architecture and capacity testing |

The complete [configuration reference](docs/reference/configuration.md), [AWS resource reference](docs/reference/aws.md), [Sendy adapter reference](docs/migration.md) and [performance evidence](docs/performance-profile.md) are embedded as expandable sections in their chapters.

The API reference is generated separately from Go definitions in `internal/httpapi`. `make build` compiles both documentation interfaces and their local assets into `bin/postbox`.
