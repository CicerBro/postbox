# Postbox interface

The interface follows a compact shadcn admin pattern: a grouped navigation rail, breadcrumb header, brand tabs, quiet slate canvas, and bordered surfaces. Indigo denotes primary actions, the active route, and current chart series. Green, amber, and red convey actual operational states. Data and comparisons always come from the API. Zero and unavailable states must remain explicit.

The desktop sidebar supports a persisted icon rail. Mobile navigation uses a Radix Dialog sheet with focus trapping and Escape handling. Creation and account menus use Radix Dropdown Menu. System, Light, and Dark remain three individually accessible icon controls; System follows live OS preferences.

Shared page classes: `page-heading` or `page-header`, `page-subtitle`, `page-tabs`, `toolbar`, `toolbar-search`, `table-card`, `table-toolbar`, `table-footer`, `row-title`, `row-subtitle`, `avatar`, `section-heading`, `empty-panel`. Badge variants: `success`, `warning`, `destructive`, `secondary`, `info`. Shared statuses can use `status-success`, `status-warning`, `status-danger`, `status-neutral`, `status-info`. The stylesheet also maps actual contact and campaign status names.

The overview retains six metrics, their API supplied comparisons, last 30 days against previous 30 days growth, and hourly UTC comparisons. Percentage point comparisons remain percentage points, and unavailable comparisons remain unavailable.
