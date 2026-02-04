package sqliteexporter

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.uber.org/zap"
)

type sqliteExporter struct {
	config *Config
	logger *zap.Logger
}

func newExporter(cfg *Config, logger *zap.Logger) *sqliteExporter {
	return &sqliteExporter{
		config: cfg,
		logger: logger,
	}
}

func (e *sqliteExporter) pushLogs(ctx context.Context, ld plog.Logs) error {
	for i := 0; i < ld.ResourceLogs().Len(); i++ {
		rl := ld.ResourceLogs().At(i)

		for j := 0; j < rl.ScopeLogs().Len(); j++ {
			sl := rl.ScopeLogs().At(j)

			for k := 0; k < sl.LogRecords().Len(); k++ {
				lr := sl.LogRecords().At(k)

				fmt.Printf("LOG: severity=%s body=%s\n",
					lr.SeverityText(),
					lr.Body().AsString(),
				)
			}
		}
	}

	return nil
}
