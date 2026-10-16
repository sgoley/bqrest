# bqrest REST API contract (v1)

This document is the normative HTTP contract for reading allowlisted BigQuery
tables and views through bqrest. Implementation tickets (PER-39 and its
dependents) conform to this document; wording uses RFC 2119 keywords.

The API is read-only and on demand. There is no scheduling, no arbitrary SQL,
and no write surface. Every response is a self-contained JSON page that a
downstream system may cache; continuation cursors let callers resume a bulk
read without re-issuing the full query.

## 1. Resources and paths

```
GET /healthz
GET /v1/connections/{connection}/resources/{dataset}/{table}/rows
```

| Segment      | Source |
|--------------|--------|
| `connection` | A configured connection `name` (config `connections[].name`). |
| `dataset`    | A dataset in that connection's `datasets` list. |
| `table`      | An allowlisted table or view (`resources[].table`) inside `dataset`. |

All three path segments are matched literally against validated configuration
before any BigQuery work happens. A request that does not match a configured
connection and allowlisted resource is rejected without touching BigQuery
(§7). Path segments are URL-decoded once; decoded values containing `/`, NUL,
or characters outside the config name alphabet never match configuration and
are rejected as `404`.

Only `GET` is defined on the rows resource. `POST`, `PUT`, `PATCH`, `DELETE`,
`HEAD`, and `OPTIONS` return `405 Method Not Allowed` with an `Allow: GET`
header. `/healthz` is unauthenticated; every `/v1/` path requires
authentication.

## 2. Authentication and authorization

Callers send a bearer credential:

```
Authorization: Bearer <token>
```

- **Static tokens** are the per-caller secrets referenced by
  `callers[].token_env`. Token comparison is constant-time.
- **OIDC/JWT** credentials are also carried as `Bearer` tokens. Issuer,
  audience, and claim validation are configuration concerns; this contract
  only fixes the wire behavior: a structurally valid JWT is never accepted in
  place of a configured static token and vice versa.

Authentication and authorization are evaluated in a fixed order so callers can
distinguish failures:

| Condition                                             | Status | Error code          |
|-------------------------------------------------------|--------|---------------------|
| Missing or malformed `Authorization` header           | 401    | `unauthenticated`   |
| Token matches no configured caller                    | 401    | `unauthenticated`   |
| Authenticated caller has no grant on `{connection}`   | 403    | `connection_denied` |
| Connection or resource is not allowlisted             | 404    | `not_found`         |

Grant checks happen before resource checks: a denied caller learns only that
the connection is not granted (`403`), never which resources exist under it.
A grant-denied response is always `403` with a non-empty `error` object; it is
never represented as a `200` with zero rows. An authorized query that matches
no rows returns `200` with `"data": []` and `meta.has_more: false`.

## 3. Request parameters

Query parameters control projection, filtering, ordering, and page size.
Parameter names are case-sensitive; unknown parameters are rejected with
`400 invalid_parameter` rather than ignored, so typos cannot silently widen a
read.

| Parameter | Meaning |
|-----------|---------|
| `select`  | Comma-separated projection of enabled columns (§4). |
| `order`   | Comma-separated `column` or `column.dir` terms (§5). |
| `limit`   | Requested maximum rows for this page (§6). |
| `cursor`  | Continuation token from a previous response (§8). |
| `{col}`   | Filter on column `{col}`; value is `{op}.{literal}` (§4). |

Repeated parameters are an error (`400 invalid_parameter`), except that a
single column may appear in only one filter.

## 4. Projection and filters

### Projection

`select` accepts a comma-separated list of top-level column names from the
resource's `enabled_columns`. Omitted `select` returns all enabled columns in
the order they are allowlisted. A name outside `enabled_columns` — including
columns that exist in BigQuery but were not exposed — is rejected
`400 invalid_projection`. Nested fields of `RECORD` columns are not
addressable in v1; a record column is projected or not, whole.

### Filter grammar

Filters follow PostgREST form: `{column}={operator}.{literal}`. Supported
operators:

| Operator  | BigQuery predicate        | Operand count |
|-----------|---------------------------|---------------|
| `eq`      | `= @p`                    | 1             |
| `neq`     | `!= @p`                   | 1             |
| `gt`      | `> @p`                    | 1             |
| `gte`     | `>= @p`                   | 1             |
| `lt`      | `< @p`                    | 1             |
| `lte`     | `<= @p`                   | 1             |
| `in`      | `IN UNNEST(@p)`           | list: `in.(a,b,c)` |
| `is`      | `IS NULL` / `IS NOT NULL` | `is.null`, `is.not_null` |
| `like`    | `LIKE @p` (`%`/`_` wildcards, STRING columns only) | 1 |

All predicates are ANDed. OR, NOT, nested logic, and arithmetic are not in v1.

### Type coercion

Literals are URL-decoded, then coerced to the filtered column's BigQuery type:

| BigQuery type            | Literal form                                |
|--------------------------|---------------------------------------------|
| `STRING`                 | Any text; used verbatim.                    |
| `INT64`, `INTEGER`       | `^-?[0-9]+$` within int64 range.            |
| `FLOAT64`, `NUMERIC`, `BIGNUMERIC` | JSON number syntax.              |
| `BOOL`                   | `true` or `false` only.                     |
| `DATE`                   | `YYYY-MM-DD`.                               |
| `DATETIME`               | `YYYY-MM-DD[ T]HH:MM:SS[.ffffff]`.          |
| `TIMESTAMP`              | RFC 3339 with offset (`Z` allowed).         |
| `JSON`, `BYTES`, `RECORD`, `ARRAY` | Filtering not supported; `400`. |

Every value is bound as a **named query parameter**; literals are never
interpolated into SQL text. A literal that fails coercion returns
`400 invalid_filter` naming the column. Filters reference only enabled
columns: an unknown or unenabled column returns `400 invalid_filter`; an
unknown operator (e.g. `month=contains.x`) returns `400 invalid_operator`.
Malformed filter syntax (no `.` separator, empty operand) is `400
invalid_filter`.

## 5. Row shape and ordering

`data` is a JSON array of objects keyed by enabled column name. Values map
from BigQuery types:

| BigQuery type              | JSON                                         |
|----------------------------|----------------------------------------------|
| `STRING`                   | string                                       |
| `INT64`, `NUMERIC`, `BIGNUMERIC` | **string** (values may exceed IEEE-754 precision) |
| `FLOAT64`                  | number (`null` for `NaN`/`±Infinity`)        |
| `BOOL`                     | boolean                                      |
| `DATE`, `TIME`, `DATETIME` | string, ISO 8601                             |
| `TIMESTAMP`                | string, RFC 3339 UTC                         |
| `BYTES`                    | string, base64                               |
| `JSON`                     | parsed JSON value                            |
| `RECORD`/`STRUCT`          | nested object, same rules recursively        |
| `ARRAY`/repeated           | JSON array                                   |
| `NULL`                     | `null`                                       |

Field names inside records appear as returned by the BigQuery schema; only the
top-level column allowlist applies.

Ordering is deterministic **within a cursor chain** (§8): all pages belong to
one immutable BigQuery job-result snapshot, so following cursors never
duplicates or skips a row. A caller that needs a specific presentation order
supplies `order` — e.g. `order=month.asc,revenue.desc` — restricted to enabled
columns (`400 invalid_order` otherwise). Without `order`, row order is
unspecified but stable for the life of the cursor chain.

## 6. Page size, response size, and bytes billed

| Guardrail | Request control | Default | Ceiling |
|-----------|-----------------|---------|---------|
| Rows per page | `limit` | 1000 | Per-request `limit` is capped at the connection's `max_page_rows`; larger values are clamped, not rejected. `limit` must be a positive integer (`400 invalid_parameter` otherwise). |
| Response bytes | none | — | A page never exceeds the connection's `max_response_bytes`. If the next row would overflow it, the page closes early (`has_more: true`) so no JSON row is ever truncated. |
| Bytes billed | none | — | Every generated query runs with `maximumBytesBilled` set to the connection's `max_bytes_billed`. BigQuery billing-limit failures surface as `413 bytes_billed_exceeded`. |

The example config (`max_page_rows: 10000`, `max_response_bytes: 10000000`,
`max_bytes_billed: 5000000000`) is the reference ceiling set; operators may
tighten per connection but the contract's `limit` default of 1000 is fixed.

## 7. Response envelope and error format

Success (`200 OK`, `Content-Type: application/json`):

```json
{
  "data": [ { "month": "2026-09-01", "revenue": "128400.50" } ],
  "meta": {
    "connection": "analytics",
    "resource": "published_insights.monthly_revenue",
    "row_count": 1000,
    "has_more": true,
    "bytes_processed": 4194304,
    "snapshot_time": "2026-10-16T14:32:07Z"
  },
  "cursor": "eyJ2IjoxLC…"
}
```

- `cursor` is present iff `has_more` is true. The last page has
  `"has_more": false` and no `cursor` field.
- `meta.bytes_processed` reports the job's processed bytes (BigQuery-billed
  basis) for the first request of a chain; `null` on continuation pages.
- `meta.snapshot_time` is the BigQuery job creation time; downstream caches
  key on it plus the request URL.
- Successful responses carry `Cache-Control: private, max-age=0, must-revalidate`
  plus an `ETag` over the URL and cursor inputs so proxies never share pages
  across callers.

Errors (`Content-Type: application/json`, matching status):

```json
{
  "error": {
    "code": "invalid_filter",
    "message": "column 'secret_field' is not an enabled column on analytics.published_insights.monthly_revenue",
    "status": 400
  }
}
```

| Status | `code`                  | Raised when |
|--------|-------------------------|-------------|
| 400    | `invalid_parameter`     | Unknown/duplicate parameter, bad `limit`, bad `cursor` syntax. |
| 400    | `invalid_projection`    | `select` names a non-enabled column. |
| 400    | `invalid_filter`        | Bad filter syntax, non-enabled column, failed coercion, unsupported filterable type. |
| 400    | `invalid_operator`      | Operator not in the supported set. |
| 400    | `invalid_order`         | `order` names a non-enabled column or bad direction. |
| 400    | `cursor_mismatch`       | Cursor bound to different connection/resource/params. |
| 401    | `unauthenticated`       | Missing, malformed, or unknown credential. |
| 403    | `connection_denied`     | Authenticated caller lacks a grant on the connection. |
| 404    | `not_found`             | Connection, dataset, or table not in the allowlist. |
| 405    | `method_not_allowed`    | Non-`GET` on a defined route (includes `Allow: GET`). |
| 409    | `cursor_expired`        | Cursor is validly formed but past expiry or its job snapshot is gone. |
| 413    | `bytes_billed_exceeded` | Query would exceed `max_bytes_billed`. |
| 429    | `rate_limited`          | Reserved; returned only if an operator enables throttling. |
| 502    | `bigquery_error`        | BigQuery rejected or failed the query (message is sanitized). |
| 503    | `unavailable`           | Gateway cannot reach BigQuery at all. |

`error.code` is stable API surface; `error.message` is human-readable and may
change. BigQuery details are summarized, never proxied verbatim with SQL text
or project internals.

## 8. Continuation cursor

### Semantics

The first request of a bulk read runs a BigQuery query job. The cursor
returned with the first page binds the continuation to that job's **immutable
result snapshot**: continuation requests do not re-run the query. Therefore a
cursor chain sees a point-in-time view — rows written to the table mid-read do
not appear, and no row is skipped or returned twice.

### Opacity and binding

The cursor is an opaque, URL-safe base64 value. Callers MUST treat it as an
atomic token: they may store it, retry with it, and send it back verbatim, but
MUST NOT parse, modify, or compose it. The server authenticates the cursor
payload (HMAC-signed or encrypted with an operator-held key) so it cannot be
forged or tampered with. The bound payload contains:

- format version,
- `{connection}/{dataset}/{table}`,
- a hash of the effective request: `select`, normalized filters, `order`,
  `limit`,
- the BigQuery job reference and next page position,
- issue and expiry timestamps.

Resending a cursor under a different caller, resource, or parameter set is
rejected `400 cursor_mismatch` — the cursor is bound to the query, not to the
caller, but callers need a grant on the connection for every continuation
request as usual.

### Expiry

Cursors expire at `min(cursor_ttl, BigQuery job-result lifetime)`; `cursor_ttl`
defaults to 1 hour and may be configured per connection up to 24 hours (the
BigQuery anonymous-results retention bound). An expired or evicted snapshot
returns `409 cursor_expired`; the caller restarts from a first-page request.

### Determinism

Within a chain, pages arrive in one fixed sequence: page *n+1*'s rows follow
page *n*'s with no overlap and no gap. Retrying a request with the same cursor
returns the same next page while the snapshot lives, so callers may resume
after network failure by replaying the last cursor they received.

## 9. Examples

Configured shape (from `config.example.json`): connection `analytics`,
dataset `published_insights`, table `monthly_revenue`, enabled columns
`month, revenue`.

### First page

```http
GET /v1/connections/analytics/resources/published_insights/monthly_revenue/rows?select=month,revenue&month=gte.2026-01-01&order=month.asc&limit=500 HTTP/1.1
Authorization: Bearer <reporting-service token>
```

`200 OK`:

```json
{
  "data": [
    { "month": "2026-01-01", "revenue": "98310.00" },
    { "month": "2026-02-01", "revenue": "104220.75" }
  ],
  "meta": {
    "connection": "analytics",
    "resource": "published_insights.monthly_revenue",
    "row_count": 500,
    "has_more": true,
    "bytes_processed": 8388608,
    "snapshot_time": "2026-10-16T14:32:07Z"
  },
  "cursor": "eyJ2IjoxLCJqb2IiOiJi...signed-payload...9f3a"
}
```

### Continuation page

```http
GET /v1/connections/analytics/resources/published_insights/monthly_revenue/rows?cursor=eyJ2IjoxLCJqb2IiOiJi...signed-payload...9f3a HTTP/1.1
Authorization: Bearer <reporting-service token>
```

`200 OK` (last page):

```json
{
  "data": [
    { "month": "2026-09-01", "revenue": "128400.50" },
    { "month": "2026-10-01", "revenue": "40210.25" }
  ],
  "meta": {
    "connection": "analytics",
    "resource": "published_insights.monthly_revenue",
    "row_count": 2,
    "has_more": false,
    "bytes_processed": null,
    "snapshot_time": "2026-10-16T14:32:07Z"
  }
}
```

Note `snapshot_time` is identical across the chain — proof both pages come
from one job snapshot. Passing `cursor` together with `select`, `order`, or a
filter parameter is a `400 invalid_parameter` error: the cursor already
carries them.

### Failure examples

Grant missing → `403`:

```json
{ "error": { "code": "connection_denied", "message": "caller is not granted connection 'analytics'", "status": 403 } }
```

Expired cursor → `409`:

```json
{ "error": { "code": "cursor_expired", "message": "cursor expired or its result snapshot is no longer retained; restart the read", "status": 409 } }
```

## 10. Explicitly out of scope for v1

- Mutations of any kind; arbitrary SQL; `OR`/`NOT`/nested filter logic.
- Row-level or per-customer filtering implied by a grant (use separate
  connections per exposure boundary).
- Cross-connection joins, `HEAD`/`Range` reads, CSV/other media types,
  gateway-side scheduling or webhooks.
- Nested-field projection/filtering into `RECORD` columns.
