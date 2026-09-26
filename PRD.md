# BigQuery REST Gateway — Product Requirements Document

## Problem Statement

Data teams need a straightforward way to expose materialized BigQuery insights to applications and external customers over REST. Giving each consumer direct BigQuery access adds IAM management burden and exposes a query-oriented warehouse interface where a simple API is preferred. A PostgREST-like interface is attractive, but this product is not intended to provide a transactional application database: BigQuery is the analytical backend, and the gateway must constrain access and query cost.

## Solution

Build a self-hosted, read-only REST gateway for BigQuery, implemented in Go and distributed first as a single Docker service. Operators use a terminal UI (TUI) to inspect consumers, connection credential references, and exposed tables/views, and to generate and edit the versioned configuration. A shared configuration-management module also powers structured command-line and local MCP interfaces so an LLM agent can inspect, validate, propose, and apply changes through the same rules. Secret values remain in runtime secret files or environment variables and are never returned by the TUI or agent tools. The service authenticates API callers with static tokens or OIDC/JWT, and grants each caller access only to configured connections. Callers query exposed resources with PostgREST-style URL filters and retrieve large result sets through cursor pagination. Hard query-cost and response-size limits protect the BigQuery project and the gateway.

The product targets data teams serving materialized insights and external consumers. It is not a substitute for a transactional database or a general-purpose SQL proxy.

## User Stories

1. As a data-team operator, I want to define a named BigQuery connection, so that the gateway can use a specific GCP identity and configuration for that data boundary.
2. As a data-team operator, I want to configure multiple named connections in one deployment, so that I can serve different datasets or customers without combining their BigQuery credentials.
3. As a data-team operator, I want to provide a service-account credential for each connection, so that the gateway can query BigQuery on its behalf.
4. As a security-conscious operator, I want credentials supplied as runtime secrets rather than committed in versioned configuration or baked into the container, so that accidental disclosure risk is reduced.
5. As a data-team operator, I want a versioned configuration file to define connections, allowlists, caller grants, and guardrails, so that configuration can be reviewed and deployed predictably.
6. As a data owner, I want to allowlist specific BigQuery tables and views, so that the gateway exposes only approved resources rather than everything an identity can access.
7. As an API consumer, I want to address an exposed table or view through a REST resource, so that I can retrieve data without using BigQuery APIs directly.
8. As an API consumer, I want PostgREST-style filtering and projection, so that I can request only matching rows and needed columns over HTTP.
9. As an API consumer, I want to retrieve large result sets in cursor-based pages, so that batch retrieval can continue without relying on costly or unstable large offsets.
10. As an API consumer, I want a continuation cursor from a response, so that I can resume retrieval of subsequent pages.
11. As an API consumer, I want requests beyond configured response limits to be rejected or bounded consistently, so that the REST contract remains predictable.
12. As an operator, I want a hard ceiling on query bytes billed, so that a request cannot exceed the configured BigQuery cost budget.
13. As an operator, I want a hard ceiling on returned rows or page size, so that large results do not overwhelm clients or the gateway.
14. As a static-token consumer, I want to authenticate with an API token, so that a machine integration can call the service without a user session.
15. As an OIDC/JWT consumer, I want to authenticate with an identity token, so that callers can use an existing identity provider rather than a separately managed static token.
16. As an operator, I want every caller credential to have explicit grants to named connections, so that authentication does not implicitly grant access to all configured data.
17. As an operator, I want a caller with no grant to receive a denied response when it requests an ungranted connection, so that connection boundaries are enforced consistently.
18. As an external customer, I want to access only the connection granted to my credential, so that my access does not require membership in the operator's GCP IAM space.
19. As an operator, I want unexposed datasets, tables, views, and columns to remain inaccessible through the API, so that the allowlist is enforced at the gateway boundary.
20. As an operator, I want unsupported or write requests rejected, so that the gateway remains read-only and cannot mutate BigQuery data.
21. As an API consumer, I want a clear error when authentication, grants, resource selection, query constraints, or BigQuery execution fails, so that I can distinguish rejected requests from successful empty results.
22. As an operator, I want to deploy the service as a single Docker service, so that self-hosting does not require a managed SaaS dependency or a distributed control plane.
23. As an operator, I want the service to use the configured BigQuery connection for each authorized request, so that credentials and dataset boundaries remain explicit in a multi-connection deployment.
24. As a security reviewer, I want the gateway's configured object allowlist to narrow the objects available through the service, so that underlying BigQuery IAM access alone does not publish data through REST.
25. As an operator, I want a TUI to view and edit connections, caller grants, credential references, and exposed resources, so that I can generate and review a valid configuration without hand-editing JSON.
26. As an operator, I want to browse BigQuery datasets, tables, and views through a configured connection, so that I can select the resources to expose.
27. As an LLM agent, I want structured local MCP tools to inspect configuration, browse resources, validate changes, preview a diff, and apply an approved change, so that I can administer bqrest using the same validation and access rules as the TUI.
28. As an operator, I want to browse tables and views in a configured dataset and enable only selected top-level columns from the TUI, so that I can define the REST exposure without editing JSON by hand.

## Implementation Decisions

- **Language:** Go (Golang).
- **BigQuery integration:** Use Google's official Go BigQuery client library (`cloud.google.com/go/bigquery`).
- **HTTP service:** Start with Go's standard `net/http`; add a routing dependency only if the API requires it.
- **Deployment:** First release is a single Docker service, self-hosted by the operator.
- **Configuration:** Use a versioned configuration file for named connections, per-connection resource allowlists and hard limits, and caller-to-connection grants. Keep private service-account key material out of version control and container images; supply it through a protected runtime secret/file mechanism.
- **Connections:** A named connection represents a BigQuery identity and its configured exposure boundary. Multiple connections are supported in one service instance.
- **Authorization:** Support static API tokens and OIDC/JWT. Credentials are granted access to explicitly selected named connections. Do not infer access to a connection from successful authentication alone.
- **Resource exposure:** Expose only operator-allowlisted BigQuery tables and views. Do not automatically expose all objects visible to the service-account identity.
- **TUI resource editing:** For a selected configured connection and dataset, load BigQuery table/view metadata; let the operator enable or hide each resource and select its enabled top-level columns. Require at least one enabled column per exposed resource and atomically save the validated allowlist.
- **API behavior:** Read-only REST resources with PostgREST-style URL filters/projection. The exact resource URL grammar and supported filter operators remain to be specified as part of the API contract.
- **Query execution:** Translate supported REST filters into parameterized BigQuery queries. Do not accept arbitrary client-supplied SQL in the initial product.
- **Paging:** Use cursor-based pagination for bulk retrieval. Cursor format, lifetime, and the exact deterministic ordering rules must be specified in the API contract; paging must not silently skip or duplicate rows when clients follow returned cursors.
- **Guardrails:** Enforce hard query-cost and response-size limits. Exact defaults and whether limits are configurable globally, per connection, or both remain to be specified.
- **Data mutations:** No insert, update, delete, or other write API.
- **Management surface:** A shared configuration-management module owns parsing, validation, redacted views, diff previews, and atomic writes. The TUI and structured CLI use this module. A local MCP server over stdio exposes bounded inspect/browse/validate/preview/apply operations for an LLM agent; it does not expose a network admin API. Configuration remains versioned and reviewable as a file.
- **Secret handling in administration:** The TUI and agent tools may show credential/token environment-variable names, mounted-file status, and non-secret identity metadata, but never secret contents. Secret creation or rotation stays with the operator's secret manager or shell environment.
- **Change application:** Configuration edits are validated and shown as a diff before writing atomically. Runtime reload behavior and whether apply requires an explicit operator confirmation are part of the admin-interface contract.

## Testing Decisions

- Tests should exercise externally observable behavior rather than implementation details: HTTP status and response behavior, authorization boundaries, resource exposure, query constraints, cost/size guardrails, and cursor continuation.
- **Highest practical seam:** Run the service through its HTTP interface with controlled BigQuery responses. Assert that public request behavior and the meaningful BigQuery request/query constraints match the contract; do not test mock forwarding or internal function calls.
- **BigQuery integration seam:** Provide opt-in integration tests against a dedicated BigQuery project to verify actual query execution, credential use, and cursor behavior that a controlled test backend cannot establish.
- Cover both authentication modes and the denial cases at connection boundaries, as well as requests that exceed configured limits.
- Repository prior art: none. The repository is empty, with no existing domain glossary, ADRs, modules, tests, or testing conventions to reuse.

## Out of Scope

- Transactional application workloads or guarantees.
- Writes or mutation endpoints.
- Arbitrary SQL submission by API callers.
- Scheduled synchronization or reverse-ETL orchestration; the initial use case is on-demand bulk retrieval of materialized insights.
- A managed SaaS control plane or hosted gateway.
- Public network admin API or hosted control plane. Local TUI, CLI, and stdio MCP administration are in scope.
- Automatically mirroring all access granted by BigQuery IAM into REST exposure.
- A PostgreSQL/PostgREST-compatible transactional semantics layer.

## Further Notes

- The intended users include application developers, data teams, and SaaS/platform teams. The initial product focus is data teams and external-customer access to materialized insights; transactional application traffic is better served by a transactional database such as PostgreSQL.
- The most important security boundary is the combination of per-connection BigQuery identity, configured resource allowlist, and explicit caller-to-connection grant. A connection grant authorizes access to that connection's exposed resources; row-level or per-customer filtering within a shared connection was not decided and must not be assumed. Use separate connections when independent BigQuery identities or data exposure boundaries are required.
- BigQuery's analytical query model differs from PostgreSQL's transactional model. “PostgREST-style” refers to REST resource access and URL filtering, not drop-in compatibility or identical query semantics.
- The working directory had no repository files or Git metadata at PRD creation time. No project glossary, ADRs, or established tests were available.
- Linear tracking: the PRD is associated with the `bqrest-project` Linear project (`P-SGO-17`). No code repository existed when the PRD was drafted.
