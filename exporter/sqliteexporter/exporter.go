package sqliteexporter

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

const insertSQL = `
  INSERT INTO logs (
      timestamp_ns, trace_id, span_id, severity_number, severity_text, body,
      service_name, host_name, instance_id, resource_attributes,
      scope_name, scope_version,
      event_type, user_id, source_ip, session_id, extra_attributes
  ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

type sqliteExporter struct {
	config *Config
	logger *zap.Logger
	db     *sql.DB
}

func newExporter(cfg *Config, logger *zap.Logger) *sqliteExporter {
	return &sqliteExporter{
		config: cfg,
		logger: logger,
	}
}

func (e *sqliteExporter) start(_ context.Context, _ component.Host) error {
	db, err := sql.Open("sqlite", e.config.Path)
	if err != nil {
		return err
	}

	e.db = db
	return nil
}

func (e *sqliteExporter) shutdown(_ context.Context) error {
	if e.db != nil {
		return e.db.Close()
	}

	return nil
}

func (e *sqliteExporter) pushLogs(_ context.Context, ld plog.Logs) error {
	tx, err := e.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)
		resAttrs := rl.Resource().Attributes()

		serviceName := attrStr(resAttrs, "service.name")
		hostName := attrStr(resAttrs, "host.name")
		instanceID := attrStr(resAttrs, "host.id")
		resourceAttrsJSON := attrsToJSON(resAttrs, "service.name", "host.name", "host.id")

		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)
			scopeName := sl.Scope().Name()
			scopeVersion := sl.Scope().Version()

			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)
				logAttrs := lr.Attributes()

				eventType := attrStr(logAttrs, "action")
				userID := attrStr(logAttrs, "user")
				sourceIP := attrStr(logAttrs, "ip_address")
				extraAttrsJSON := attrsToJSON(logAttrs, "action", "user", "ip_address")

				_, err = stmt.Exec(
					int64(lr.Timestamp()),
					encodeTraceID(lr.TraceID()),
					encodeSpanID(lr.SpanID()),
					int(lr.SeverityNumber()),
					lr.SeverityText(),
					lr.Body().AsString(),
					serviceName,
					hostName,
					instanceID,
					resourceAttrsJSON,
					scopeName,
					scopeVersion,
					eventType,
					userID,
					sourceIP,
					"",
					extraAttrsJSON,
				)
				if err != nil {
					return err
				}
			}
		}
	}

	return tx.Commit()
}

func attrStr(attrs pcommon.Map, key string) string {
	v, ok := attrs.Get(key)
	if !ok {
		return ""
	}
	return v.AsString()
}

func attrsToJSON(attrs pcommon.Map, exclude ...string) string {
	excludeSet := make(map[string]struct{}, len(exclude))
	for _, k := range exclude {
		excludeSet[k] = struct{}{}
	}
	m := make(map[string]any)
	attrs.Range(func(k string, v pcommon.Value) bool {
		if _, skip := excludeSet[k]; !skip {
			m[k] = v.AsRaw()
		}
		return true
	})
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func encodeTraceID(id pcommon.TraceID) string {
	if id.IsEmpty() {
		return ""
	}
	return hex.EncodeToString(id[:])
}

func encodeSpanID(id pcommon.SpanID) string {
	if id.IsEmpty() {
		return ""
	}
	return hex.EncodeToString(id[:])
}
