package telemetry

import (
	"context"
	"database/sql"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ObserveDBPool publishes connection-pool statistics for a *sql.DB.
//
// # Why this is the interesting pool
//
// The write pool has exactly ONE connection, deliberately: SQLite takes a
// database-wide write lock, so a second writer would only queue at a lower
// level and fail less legibly. Everything that mutates therefore serialises
// through a single connection.
//
// That is a correct design with one consequence nobody could see: when writes
// back up, callers block in `database/sql` waiting for the connection, and from
// the outside that is indistinguishable from the application being slow. The
// RPC latency histogram shows calls getting slower and says nothing about why.
// `WaitCount` and `WaitDuration` are the two numbers that separate "the server
// is busy" from "everything is queued behind one writer" — the second is a
// tuning problem with an answer, and until now it looked exactly like the
// first, which does not.
//
// # Gauges rather than a callback per metric
//
// One registered callback reads Stats() once and reports every series from that
// snapshot, so the numbers agree with each other. Reading the struct separately
// per instrument would let in-use and idle come from different instants and
// occasionally sum to more than the pool holds, which is the kind of impossible
// reading that costs an hour.
//
// `pool` is a bounded label — "read" or "write" — and carries nothing about
// what is being stored.
func ObserveDBPool(m metric.Meter, name string, db *sql.DB) error {
	open, err := m.Int64ObservableGauge("articleflux.db.connections.open",
		metric.WithDescription("Connections currently established, in use or idle."))
	if err != nil {
		return err
	}
	inUse, err := m.Int64ObservableGauge("articleflux.db.connections.in_use",
		metric.WithDescription("Connections currently in use."))
	if err != nil {
		return err
	}
	waits, err := m.Int64ObservableCounter("articleflux.db.wait.count",
		metric.WithDescription("Times a caller had to wait for a connection."))
	if err != nil {
		return err
	}
	waitSeconds, err := m.Float64ObservableCounter("articleflux.db.wait.seconds",
		metric.WithDescription("Total time callers spent waiting for a connection."),
		metric.WithUnit("s"))
	if err != nil {
		return err
	}

	attrs := metric.WithAttributes(attribute.String("pool", name))
	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := db.Stats()
		o.ObserveInt64(open, int64(s.OpenConnections), attrs)
		o.ObserveInt64(inUse, int64(s.InUse), attrs)
		o.ObserveInt64(waits, s.WaitCount, attrs)
		o.ObserveFloat64(waitSeconds, s.WaitDuration.Seconds(), attrs)
		return nil
	}, open, inUse, waits, waitSeconds)
	return err
}
