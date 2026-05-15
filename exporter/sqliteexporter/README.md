# SQLite Schema Design for Local Log Storage

## Overview

This document outlines the schema design for storing OpenTelemetry-formatted access logs locally on each server before pull-based export to a central audit system.

## Context

We are building a pull-based audit logging system where:

- Multiple servers generate access logs (login, logout, failed attempts)
- Logs are stored locally in SQLite until an admin triggers a pull
- On pull, logs are exported to a central Grafana Loki-compatible backend

The local SQLite database serves as a buffer, storing OTEL-formatted logs until export.

## Design Goals

1. **OTEL Compatibility** — Schema must map cleanly to/from OTLP log format
2. **Query Performance** — Support fast queries by time, user, and event type
3. **Export Efficiency** — Quickly identify and retrieve unexported logs
4. **Simplicity** — Minimize complexity in the exporter implementation

## Schema

```sql
CREATE TABLE logs (
    -- Identity
    log_id INTEGER PRIMARY KEY AUTOINCREMENT,
    
    -- Timestamps
    timestamp_ns INTEGER NOT NULL,
    timestamp TEXT GENERATED ALWAYS AS (datetime(timestamp_ns / 1000000000, 'unixepoch')),
    
    -- OTEL Core Fields
    trace_id TEXT,
    span_id TEXT,
    severity_number INTEGER,
    severity_text TEXT,
    body TEXT NOT NULL,
    
    -- Resource (denormalized)
    service_name TEXT NOT NULL,
    host_name TEXT NOT NULL,
    instance_id TEXT,
    resource_attributes TEXT,
    
    -- Scope
    scope_name TEXT,
    scope_version TEXT,
    
    -- Log Attributes (audit fields)
    event_type TEXT,
    user_id TEXT,
    source_ip TEXT,
    session_id TEXT,
    extra_attributes TEXT,
    
    -- Export tracking
    exported_at INTEGER DEFAULT NULL
);

CREATE INDEX idx_timestamp ON logs(timestamp_ns);
CREATE INDEX idx_service_host ON logs(service_name, host_name);
CREATE INDEX idx_user ON logs(user_id);
CREATE INDEX idx_event_type ON logs(event_type);
CREATE INDEX idx_exported ON logs(exported_at);
```

## Key Design Decisions

### 1. Single Table vs Normalized Tables

**Decision:** Single denormalized table

**Alternatives considered:**
- Two tables: `resources` (resource metadata) + `logs` (log records with foreign key)
- Three tables: Adding a `scopes` table

**Why single table:**

| Factor | Single Table | Normalized |
|--------|--------------|------------|
| Insert complexity | Simple | Requires upsert + FK lookup |
| Export query | No joins | Requires joins |
| Storage overhead | Minimal duplication | Slightly less |
| Code complexity | Lower | Higher |

For our use case, resource metadata (service_name, host_name) is constant per collector instance. The "duplication" is ~50 bytes repeated per row — negligible compared to the complexity cost of normalization.

**Industry precedent:** ClickHouse's official OTEL exporter uses a single denormalized table with `ResourceAttributes` as a Map column, not a separate table.

### 2. Dedicated Columns vs JSON for Log Attributes

**Decision:** Hybrid approach — dedicated columns for queried fields, JSON for the rest

**Fields as columns:**
- `event_type` — filtered in every audit query
- `user_id` — "show all events for user X"
- `source_ip` — "show events from IP Y"  
- `session_id` — "show session activity"

**Fields as JSON (`extra_attributes`):**
- `user_agent`, `auth_method`, `geo.country`, etc.

**Why hybrid:**

```sql
-- Fast (indexed column)
SELECT * FROM logs WHERE user_id = 'u123';

-- Slow (JSON extraction, no index)
SELECT * FROM logs WHERE json_extract(extra_attributes, '$.user_id') = 'u123';
```

Fields we query become columns. Fields we just preserve stay in JSON.

### 3. Nanosecond Timestamps

**Decision:** Store as `INTEGER` (nanoseconds since epoch)

**Why:**
- OTEL specification uses nanosecond precision
- Integer comparison is faster than string comparison
- Preserves full precision for export

**Convenience:** A generated column provides human-readable timestamps for debugging without storage overhead.

### 4. Export Tracking with `exported_at`

**Decision:** Nullable timestamp column

**How it works:**
- `NULL` = not yet exported
- Timestamp = when exported

**Why timestamp instead of boolean:**
- Enables queries like "re-export logs from failed pull at time X"
- Provides audit trail of export history
- Supports incremental/partial exports

### 5. Index Selection

| Index | Purpose | Query Pattern |
|-------|---------|---------------|
| `idx_timestamp` | Time-range queries | `WHERE timestamp_ns BETWEEN ? AND ?` |
| `idx_service_host` | Filter by source | `WHERE service_name = ? AND host_name = ?` |
| `idx_user` | User audit queries | `WHERE user_id = ?` |
| `idx_event_type` | Event filtering | `WHERE event_type = 'failed_attempt'` |
| `idx_exported` | Find unexported logs | `WHERE exported_at IS NULL` |

**Not indexed:**
- `body` — Would need FTS5 for text search; filter by other fields first
- `resource_attributes`, `extra_attributes` — JSON blobs can't be indexed efficiently
- `severity_*` — Low cardinality (~6 values); index wouldn't help

## OTEL Field Mapping

| OTEL LogRecord Field | Schema Column | Notes |
|---------------------|---------------|-------|
| `timeUnixNano` | `timestamp_ns` | — |
| `traceId` | `trace_id` | Hex-encoded |
| `spanId` | `span_id` | Hex-encoded |
| `severityNumber` | `severity_number` | 1-24 per OTEL spec |
| `severityText` | `severity_text` | INFO, WARN, ERROR, etc. |
| `body` | `body` | Log message |
| Resource attributes | `service_name`, `host_name`, `instance_id`, `resource_attributes` | Common fields extracted |
| Scope name/version | `scope_name`, `scope_version` | — |
| Log attributes | `event_type`, `user_id`, `source_ip`, `session_id`, `extra_attributes` | Audit fields extracted |

## Expected Query Patterns

**1. Export unexported logs:**
```sql
SELECT * FROM logs 
WHERE exported_at IS NULL 
ORDER BY timestamp_ns;
```

**2. Audit query — user activity:**
```sql
SELECT * FROM logs 
WHERE user_id = ? 
  AND timestamp_ns BETWEEN ? AND ?
ORDER BY timestamp_ns;
```

**3. Audit query — failed logins:**
```sql
SELECT * FROM logs 
WHERE event_type = 'failed_attempt'
  AND timestamp_ns BETWEEN ? AND ?
ORDER BY timestamp_ns;
```

**4. Audit query — activity from IP:**
```sql
SELECT * FROM logs 
WHERE source_ip = ?
  AND timestamp_ns BETWEEN ? AND ?
ORDER BY timestamp_ns;
```

## Trade-offs Acknowledged

| Trade-off | Mitigation |
|-----------|------------|
| Resource fields duplicated per row | Minimal overhead (~50 bytes); simpler code |
| JSON fields not queryable efficiently | Promote to column if query pattern emerges |
| No full-text search on `body` | Can add FTS5 virtual table later if needed |
| Single-node SQLite (no replication) | Acceptable for local buffer; central system handles durability |

## Future Considerations

- **FTS5 for body search:** If full-text search on log messages is needed, add a virtual table
- **Compression:** SQLite doesn't compress by default; consider if storage becomes an issue
- **Retention/cleanup:** Add a job to delete old exported logs if disk space is constrained
- **Schema migrations:** If fields change, use a migration tool or versioned schema

## References

- [OpenTelemetry Log Data Model](https://opentelemetry.io/docs/specs/otel/logs/data-model/)
- [ClickHouse OTEL Schema](https://clickhouse.com/docs/use-cases/observability/clickstack/ingesting-data/schemas)
- [ClickHouse Exporter Design](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/exporter/clickhouseexporter)
