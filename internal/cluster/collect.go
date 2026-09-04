package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	// The MySQL driver is the only dependency in the repository, and it is
	// registered here so that every database access in the tool goes through
	// this file — where the read-only guard is.
	_ "github.com/go-sql-driver/mysql"
)

// readOnly matches the only two statements this tool is allowed to send.
//
// galera-doctor audits a production cluster, frequently one that is already
// having a bad day, and the promise that it cannot change anything has to be
// mechanical rather than a claim in a README. Every query goes through Query,
// Query refuses anything that is not a SHOW or a SELECT, and CI greps the
// source for a writing statement.
var readOnly = regexp.MustCompile(`(?is)^\s*(SHOW|SELECT)\s`)

// ErrNotReadOnly is returned for a query that is not a SHOW or a SELECT.
type ErrNotReadOnly struct{ Query string }

func (e ErrNotReadOnly) Error() string {
	return fmt.Sprintf("refused: galera-doctor only issues SHOW and SELECT, got %q", firstWords(e.Query, 6))
}

// Query is the only way this package talks to a server.
func Query(ctx context.Context, db *sql.DB, q string, args ...any) (*sql.Rows, error) {
	if !readOnly.MatchString(q) {
		return nil, ErrNotReadOnly{Query: q}
	}
	return db.QueryContext(ctx, q, args...)
}

// Collector reads nodes. The zero value is usable.
type Collector struct {
	// Timeout bounds each node, not the whole run: one unreachable node must
	// not decide how long the audit takes.
	Timeout time.Duration
	// SkipSysTables and SkipSchema turn off the two information_schema reads,
	// which are the only parts that cost more than microseconds on a large
	// instance.
	SkipSysTables bool
	SkipSchema    bool
	// Now is the clock; tests set it.
	Now func() time.Time
}

// SystemSchemas are never audited for missing primary keys: they are the
// server's own, they are not replicated the same way, and reporting them would
// bury the application tables that matter.
var SystemSchemas = []string{"mysql", "information_schema", "performance_schema", "sys"}

// Collect reads one node. A failure is returned inside the Snapshot rather
// than as an error: a node that cannot be read is a finding, and the audit
// still has to run over the nodes that could.
func (c Collector) Collect(ctx context.Context, n Node) Snapshot {
	snap := Snapshot{Node: n.Name, At: c.now()}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	db, err := sql.Open("mysql", n.DSN)
	if err != nil {
		snap.Err = redact(err.Error(), n.DSN)
		return snap
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		snap.Err = redact(err.Error(), n.DSN)
		return snap
	}

	// The clock first, and stamped with this host's time immediately before the
	// round trip: the skew this measures is only as good as the gap between the
	// two readings, and reading it after 1200 status variables would fold the
	// server's own response time into the number.
	snap.At = c.now()
	if snap.Clock, err = serverClock(ctx, db); err != nil {
		// A server that will not answer this is not a server whose clock
		// agrees: the zero value means "not read" and the check says so.
		snap.Clock = time.Time{}
		_ = err
	}

	if snap.Status, err = keyValue(ctx, db, "SHOW GLOBAL STATUS"); err != nil {
		snap.Err = redact(err.Error(), n.DSN)
		return snap
	}
	if snap.Vars, err = keyValue(ctx, db, "SHOW GLOBAL VARIABLES"); err != nil {
		snap.Err = redact(err.Error(), n.DSN)
		return snap
	}
	if !c.SkipSysTables {
		if snap.SysTables, err = sysTableFingerprints(ctx, db); err != nil {
			// A missing grant on information_schema must not throw away the
			// status read that already succeeded: the drift check reports the
			// gap, the rest of the audit continues.
			snap.SysTables = nil
			snap.Err = ""
			_ = err
		}
	}
	if !c.SkipSchema {
		// AppTables stays nil when the read fails: the audit reports a node it
		// could not compare, which is not the same statement as "this node has
		// no application tables".
		if snap.AppTables, err = appTableFingerprints(ctx, db); err != nil {
			snap.AppTables = nil
			_ = err
		}
		if snap.TablesNoPK, err = tablesWithoutPK(ctx, db); err != nil {
			snap.TablesNoPK = nil
			_ = err
		}
		if snap.TablesNonInnoDB, err = tablesNotReplicated(ctx, db); err != nil {
			snap.TablesNonInnoDB = nil
			_ = err
		}
		if snap.DataBytes, err = datasetBytes(ctx, db); err != nil {
			snap.DataBytes = nil
			_ = err
		}
	}
	return snap
}

// CollectAll reads every node concurrently. Nodes are independent and a slow
// one must not delay the rest; the results keep the input order so a report is
// stable across runs.
func (c Collector) CollectAll(ctx context.Context, nodes []Node) []Snapshot {
	snaps := make([]Snapshot, len(nodes))
	done := make(chan int, len(nodes))
	for i, n := range nodes {
		go func(i int, n Node) {
			snaps[i] = c.Collect(ctx, n)
			done <- i
		}(i, n)
	}
	for range nodes {
		<-done
	}
	return snaps
}

func (c Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// serverClock reads the server's own clock as a Unix timestamp (GD-16).
//
// UNIX_TIMESTAMP(NOW(6)) rather than NOW(): an epoch is the same number
// whatever the server's time_zone is set to, and the microseconds matter
// because a sub-second skew is inside the round trip.
func serverClock(ctx context.Context, db *sql.DB) (time.Time, error) {
	rows, err := Query(ctx, db, "SELECT UNIX_TIMESTAMP(NOW(6))")
	if err != nil {
		return time.Time{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return time.Time{}, err
		}
		return time.Time{}, fmt.Errorf("no row from UNIX_TIMESTAMP(NOW(6))")
	}
	var epoch float64
	if err := rows.Scan(&epoch); err != nil {
		return time.Time{}, err
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, err
	}
	sec, frac := math.Modf(epoch)
	return time.Unix(int64(sec), int64(frac*float64(time.Second))).UTC(), nil
}

// keyValue reads a two-column SHOW into a lowercase-keyed map.
func keyValue(ctx context.Context, db *sql.DB, q string) (map[string]string, error) {
	rows, err := Query(ctx, db, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v sql.NullString
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[strings.ToLower(k.String)] = v.String
	}
	return out, rows.Err()
}

// sysTableFingerprints hashes the column definitions of every table in the
// `mysql` schema.
//
// This is the check that Galera's own metrics cannot make. DDL against the
// system tables does not travel the way application DDL does, so two nodes can
// disagree about the definition of mysql.column_stats for months while every
// wsrep_* counter reports a perfectly healthy cluster — until a query plan, a
// mysql_upgrade or a backup restore trips over it.
func sysTableFingerprints(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := Query(ctx, db, `
		SELECT TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION, COLUMN_TYPE,
		       IS_NULLABLE, COLUMN_KEY, IFNULL(COLUMN_DEFAULT, '~none~')
		  FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = 'mysql'
		 ORDER BY TABLE_NAME, ORDINAL_POSITION`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	perTable := map[string][]string{}
	for rows.Next() {
		var table, column, colType, nullable, key, def string
		var ordinal int
		if err := rows.Scan(&table, &column, &ordinal, &colType, &nullable, &key, &def); err != nil {
			return nil, err
		}
		perTable[table] = append(perTable[table], ColumnRow(column, ordinal, colType, nullable, key, def))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(perTable))
	for table, defs := range perTable {
		out[table] = Fingerprint(defs)
	}
	return out, nil
}

// appTableFingerprints hashes the column definitions of every application base
// table, keyed schema.table (GD-13).
//
// Galera does replicate this DDL, so a difference between nodes is not the
// invisible-maintenance story of sysTableFingerprints: it is a schema change
// that did not finish. The counters stay green either way, because replication
// did carry what it was given.
//
// Views are excluded on purpose — information_schema.COLUMNS happily expands
// them, and a view's columns are derived from tables that are already being
// compared.
func appTableFingerprints(ctx context.Context, db *sql.DB) (map[string]string, error) {
	args := make([]any, 0, len(SystemSchemas))
	for _, s := range SystemSchemas {
		args = append(args, s)
	}
	rows, err := Query(ctx, db, `
		SELECT c.TABLE_SCHEMA, c.TABLE_NAME, c.COLUMN_NAME, c.ORDINAL_POSITION,
		       c.COLUMN_TYPE, c.IS_NULLABLE, c.COLUMN_KEY,
		       IFNULL(c.COLUMN_DEFAULT, '~none~')
		  FROM information_schema.COLUMNS c
		  JOIN information_schema.TABLES t
		    ON t.TABLE_SCHEMA = c.TABLE_SCHEMA
		   AND t.TABLE_NAME = c.TABLE_NAME
		 WHERE t.TABLE_TYPE = 'BASE TABLE'
		   AND c.TABLE_SCHEMA NOT IN (`+placeholders(len(SystemSchemas))+`)
		 ORDER BY c.TABLE_SCHEMA, c.TABLE_NAME, c.ORDINAL_POSITION`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	perTable := map[string][]string{}
	for rows.Next() {
		var schema, table, column, colType, nullable, key, def string
		var ordinal int
		if err := rows.Scan(&schema, &table, &column, &ordinal, &colType, &nullable, &key, &def); err != nil {
			return nil, err
		}
		name := schema + "." + table
		perTable[name] = append(perTable[name], ColumnRow(column, ordinal, colType, nullable, key, def))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Non-nil even when the server has no application tables at all: the audit
	// reads nil as "not audited".
	out := make(map[string]string, len(perTable))
	for table, defs := range perTable {
		out[table] = Fingerprint(defs)
	}
	return out, nil
}

// tablesWithoutPK lists application base tables with no primary key.
func tablesWithoutPK(ctx context.Context, db *sql.DB) ([]string, error) {
	args := make([]any, 0, len(SystemSchemas))
	for _, s := range SystemSchemas {
		args = append(args, s)
	}
	rows, err := Query(ctx, db, `
		SELECT t.TABLE_SCHEMA, t.TABLE_NAME
		  FROM information_schema.TABLES t
		  LEFT JOIN information_schema.TABLE_CONSTRAINTS c
		         ON c.TABLE_SCHEMA = t.TABLE_SCHEMA
		        AND c.TABLE_NAME = t.TABLE_NAME
		        AND c.CONSTRAINT_TYPE = 'PRIMARY KEY'
		 WHERE t.TABLE_TYPE = 'BASE TABLE'
		   AND t.TABLE_SCHEMA NOT IN (`+placeholders(len(SystemSchemas))+`)
		   AND c.CONSTRAINT_NAME IS NULL
		 ORDER BY t.TABLE_SCHEMA, t.TABLE_NAME`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			return nil, err
		}
		out = append(out, schema+"."+table)
	}
	sort.Strings(out)
	return out, rows.Err()
}

// ReplicatedEngines are the storage engines Galera replicates. Everything else
// in an application schema is per-node data wearing a cluster's clothes: the
// write succeeds, nothing certifies it, and no counter records that it went
// nowhere.
var ReplicatedEngines = []string{"InnoDB"}

// tablesNotReplicated lists application base tables on an engine Galera does
// not replicate, rendered "schema.table (ENGINE)" (GD-29).
func tablesNotReplicated(ctx context.Context, db *sql.DB) ([]string, error) {
	args := make([]any, 0, len(SystemSchemas)+len(ReplicatedEngines))
	for _, s := range SystemSchemas {
		args = append(args, s)
	}
	for _, e := range ReplicatedEngines {
		args = append(args, e)
	}
	rows, err := Query(ctx, db, `
		SELECT TABLE_SCHEMA, TABLE_NAME, ENGINE
		  FROM information_schema.TABLES
		 WHERE TABLE_TYPE = 'BASE TABLE'
		   AND ENGINE IS NOT NULL
		   AND TABLE_SCHEMA NOT IN (`+placeholders(len(SystemSchemas))+`)
		   AND ENGINE NOT IN (`+placeholders(len(ReplicatedEngines))+`)
		 ORDER BY TABLE_SCHEMA, TABLE_NAME`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var schema, table, engine string
		if err := rows.Scan(&schema, &table, &engine); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%s.%s (%s)", schema, table, engine))
	}
	return out, rows.Err()
}

// placeholders is n comma-separated question marks.
func placeholders(n int) string {
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

// datasetBytes is what a full state transfer would have to copy: the data and
// index length of every application table (GD-41).
//
// information_schema reports these per storage engine and they are an estimate
// for InnoDB, not a file size — which is the right order of magnitude for the
// only question being asked ("how long will this node be copying, and how long
// is a donor out of service"). nil rather than 0 when the read fails: a size of
// zero would make an SST look free.
func datasetBytes(ctx context.Context, db *sql.DB) (*int64, error) {
	args := make([]any, 0, len(SystemSchemas))
	for _, s := range SystemSchemas {
		args = append(args, s)
	}
	rows, err := Query(ctx, db, `
		SELECT SUM(DATA_LENGTH + INDEX_LENGTH)
		  FROM information_schema.TABLES
		 WHERE TABLE_TYPE = 'BASE TABLE'
		   AND TABLE_SCHEMA NOT IN (`+placeholders(len(SystemSchemas))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("no row from the dataset size query")
	}
	// SUM over no rows is NULL, which is a real answer: an empty cluster.
	var total sql.NullFloat64
	if err := rows.Scan(&total); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	bytes := int64(0)
	if total.Valid {
		bytes = int64(total.Float64)
	}
	return &bytes, nil
}

// redact keeps a DSN — and therefore a password — out of an error message. A
// driver error quotes the DSN it was given, and error messages end up in
// tickets and Slack.
func redact(msg, dsn string) string {
	if dsn == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, dsn, "<dsn>")
	if at := strings.LastIndex(dsn, "@"); at > 0 {
		if creds := dsn[:at]; creds != "" {
			msg = strings.ReplaceAll(msg, creds, "<credentials>")
			if _, pass, ok := strings.Cut(creds, ":"); ok && pass != "" {
				msg = strings.ReplaceAll(msg, pass, "<password>")
			}
		}
	}
	return msg
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}
