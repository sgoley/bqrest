<p align="center">
  <img src="assets/bqrest-logo.png" alt="bqrest logo combining Bucharest's blue-yellow-red tricolor with a BigQuery-style analytics magnifier" width="190">
</p>

<h1 align="center">bqrest</h1>

<p align="center">BigQuery insights, RESTfully served.</p>

<p align="center">
  <a href="https://github.com/sgoley/bqrest/actions/workflows/ci.yml"><img src="https://github.com/sgoley/bqrest/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI status"></a>
</p>

<p align="center">
  <a href="https://deploy.cloud.run?git_repo=https%3A%2F%2Fgithub.com%2Fsgoley%2Fbqrest">
    <img src="https://deploy.cloud.run/button.svg" alt="Deploy to Google Cloud Run">
  </a>
</p>

`bqrest` is a self-hosted, read-only REST gateway for allowlisted BigQuery insights. It is designed for on-demand, cursor-paged bulk reads that another system can cache. It does not expose arbitrary SQL or provide a scheduled sync service.

## Current status

The initial bootstrap serves `GET /healthz`, validates the versioned JSON configuration, and includes a read-only TUI for connections, consumer grants, credential-reference status, and exposed resources. BigQuery reads and configuration generation are tracked in the [Linear project](https://linear.app/sgoley/project/bqrest-project-beb1a335a494/overview).

## Try the Cloud Run demo

Use the button above to deploy a private, health-only bootstrap to your Google Cloud project. The deploy form uses the repository's `app.json`; its demo flag lets the container start without a BigQuery config or credentials. Verify it with an authenticated request to `/healthz`. The BigQuery REST query endpoints are still under development, so this button tests deployment and service startup rather than serving table data.

## Configuration

Copy `config.example.json` and set the environment variables it references. Credential environment variables contain paths to mounted service-account files; API token variables contain the caller tokens themselves. Keep credential files and tokens outside version control and the Docker image.

Each connection requires a project, location, configured datasets, allowlisted tables/views with explicit enabled columns, and positive query/output limits. Each caller token must explicitly grant at least one configured connection. The config loader rejects unknown keys, duplicate names or grants, missing runtime secrets, resources outside configured datasets, and empty/duplicate column allowlists.

## Run locally

```sh
export BQREST_ANALYTICS_CREDENTIALS_FILE=/run/secrets/bq-analytics.json
export BQREST_REPORTING_TOKEN='replace-with-a-long-random-token'
go run ./cmd/bqrest -config ./config.example.json
```

Then check health:

```sh
curl http://localhost:8080/healthz
```

View the configured connections, consumers, safe credential-reference status, and allowlisted tables in the TUI:

```sh
go run ./cmd/bqrest tui -config ./config.example.json
```

In the Resources view, press `r` to load tables and views from BigQuery for the selected configured dataset. Use `c` and `d` to change connection and dataset, arrows to select a resource, Space to expose/hide it, Enter to edit enabled top-level columns, and `s` to validate and atomically save the config. The BigQuery identity needs permission to list dataset tables and read their metadata. Restart the gateway to load the changed allowlist. The TUI never prints secret values or credential file paths.

## CI and releases

Pull requests and pushes to `main` or `master` run formatting, `go vet`, race-enabled tests, a Go build, and a Docker build. Push a version tag such as `v1.2.3` to a commit on either branch to create a GitHub Release and publish `ghcr.io/sgoley/bqrest:v1.2.3`; stable tags also update `latest`.

## Run in Docker

```sh
docker build -t bqrest:local .
docker run --rm -p 8080:8080 \
  -v "$PWD/config.example.json:/etc/bqrest/config.json:ro" \
  -v /secure/path/bq-analytics.json:/run/secrets/bq-analytics.json:ro \
  -e BQREST_ANALYTICS_CREDENTIALS_FILE=/run/secrets/bq-analytics.json \
  -e BQREST_REPORTING_TOKEN="$BQREST_REPORTING_TOKEN" \
  bqrest:local
```
