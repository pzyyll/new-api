# Add Request Detail Logs Implementation Plan

## Goal

Implement request JSON body logging for usage log details.

The feature lets administrators enable or disable request body recording from Log Maintenance settings. When enabled, consume and error logs persist the full incoming JSON request body and expose it in the existing usage log details dialog.

## Confirmed Decisions

- Store the complete request JSON body without truncation.
- Display the request JSON body with `react-json-view`.
- Return `request_body` through the existing usage log list APIs.
- Keep the feature disabled by default to preserve current privacy and storage behavior.

## Current State

The existing `logs` table is represented by `model.Log` in `model/log.go`.

Current persisted fields include:

- `content`: summary text or masked error text.
- `other`: JSON metadata for billing, admin info, request path, conversion chain, stream status, and related details.
- `request_id` / `upstream_request_id`: request correlation identifiers.

The full request JSON body is not persisted today. DEBUG mode logs raw request bodies through `middleware/debug_logger.go`, but that is application logging only and does not write to the database.

## Backend Plan

### 1. Add Runtime Option

Files:

- `common/constants.go`
- `model/option.go`

Add a new global option:

```go
var RequestDetailLogEnabled = false
```

Register it in option defaults:

```go
common.OptionMap["RequestDetailLogEnabled"] = strconv.FormatBool(common.RequestDetailLogEnabled)
```

Handle updates in the boolean option switch:

```go
case "RequestDetailLogEnabled":
    common.RequestDetailLogEnabled = boolValue
```

### 2. Add Log Model Field

File:

- `model/log.go`

Add a dedicated field instead of embedding the request body in `other`:

```go
RequestBody string `json:"request_body,omitempty"`
```

`AutoMigrate(&Log{})` already runs for both main DB migration and `LOG_DB`, so the new column should be created automatically for SQLite, MySQL, and PostgreSQL.

### 3. Extend Log Write Parameters

File:

- `model/log.go`

Update:

- `RecordConsumeLogParams`
- `RecordConsumeLog`
- `RecordErrorLog`

Add `RequestBody string` to consume/error log writes and map it to `Log.RequestBody`.

### 4. Add Request Body Capture Helper

Recommended file:

- `service/request_body_log.go`

Add a helper:

```go
func CaptureRequestBodyForLog(ctx *gin.Context) string
```

Behavior:

- Return an empty string when `common.RequestDetailLogEnabled` is false.
- Use `common.GetBodyStorage(ctx)` to read the reusable request body.
- Reset storage position after reading.
- Store only JSON request bodies.
- Validate JSON with project JSON wrappers, e.g. `common.Unmarshal` into `any`.
- Return the original raw JSON string to preserve the exact request body.
- Do not truncate.

### 5. Populate Consume Logs

File:

- `service/text_quota.go`

In `PostTextConsumeQuota`, capture the request body before `model.RecordConsumeLog(...)` and pass it into `RecordConsumeLogParams`.

### 6. Populate Error Logs

File:

- `controller/relay.go`

In `processChannelError`, capture the request body and pass it into `model.RecordErrorLog(...)`.

This ensures upstream failure logs can also expose request details when error logging and request detail logging are enabled.

### 7. API Behavior

Existing endpoints remain unchanged:

- `GET /api/log/`
- `GET /api/log/self`

Because `model.Log` is returned directly, adding `request_body` to the JSON model exposes it through the existing list payload.

## Frontend Plan

### 1. Add Dependency

From `web/default`:

```bash
bun add react-json-view
```

If TypeScript definitions are required:

```bash
bun add -d @types/react-json-view
```

### 2. Update Usage Log Schema

File:

- `web/default/src/features/usage-logs/data/schema.ts`

Add:

```ts
request_body: z.string().default('')
```

### 3. Render Request Body in Details Dialog

File:

- `web/default/src/features/usage-logs/components/dialogs/details-dialog.tsx`

Add a `Request JSON Body` section.

Behavior:

- Parse `props.log.request_body` with `JSON.parse`.
- Render parsed JSON with `react-json-view`.
- Include copy-to-clipboard support using the existing dialog copy pattern.
- If parsing fails, fall back to raw body display.
- Keep the section visually consistent with existing detail sections.

### 4. Add Log Maintenance Switch

Files:

- `web/default/src/features/system-settings/types.ts`
- `web/default/src/features/system-settings/operations/index.tsx`
- `web/default/src/features/system-settings/operations/section-registry.tsx`
- `web/default/src/features/system-settings/maintenance/log-settings-section.tsx`
- `web/default/src/features/system-settings/hooks/use-update-option.ts`

Add `RequestDetailLogEnabled` next to `LogConsumeEnabled`.

Suggested UI text:

- Label: `Record request JSON body`
- Description: `Store full request JSON bodies for usage log details. This can significantly increase database size and may contain sensitive user-provided content.`

### 5. Internationalization

Add or sync translation keys for:

- `Record request JSON body`
- `Store full request JSON bodies for usage log details. This can significantly increase database size and may contain sensitive user-provided content.`
- `Request JSON Body`
- `Invalid JSON; showing raw request body.`

## Verification Plan

### Backend

Run:

```bash
go test ./...
```

Manual checks:

- Disable `RequestDetailLogEnabled`, send a request, and confirm `request_body` is empty.
- Enable `RequestDetailLogEnabled`, send a JSON request, and confirm `request_body` is persisted.
- Trigger an upstream error with error logging enabled and confirm the error log stores `request_body`.

### Frontend

From `web/default`:

```bash
bun run typecheck
```

If available:

```bash
bun run lint
```

Manual checks:

- Open System Settings -> Operations -> Log Maintenance.
- Toggle request body logging and save.
- Send a request.
- Open Usage Logs -> Details.
- Confirm the JSON viewer renders the request body.
- Confirm copy-to-clipboard works.

## Risks

- Full request bodies can contain sensitive user-provided content.
- Full request bodies can significantly increase `logs` table size.
- Existing list APIs will return larger payloads when logs contain request bodies.
- Multipart/form-data requests are out of scope for this first implementation unless their body can be represented as valid JSON.
- Existing historical logs will not have `request_body`.

## Rollout

Default the option to disabled. Administrators must explicitly enable request body logging from Log Maintenance settings.
