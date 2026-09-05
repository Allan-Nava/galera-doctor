// Package config reads the cluster definitions.
//
// JSON, not YAML, and that is a deliberate trade: the repository has exactly
// one dependency — the MySQL driver — and a config format is not worth a
// second one. The file is small by design, and every DSN in it can be written
// as ${ENV_VAR} so a password never has to live on disk next to the topology.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/cluster"
)

// Cluster is one cluster to audit.
type Cluster struct {
	Nodes []cluster.Node `json:"nodes"`
	// ProxySQLDSN is the admin interface in front of this cluster. Optional:
	// without it the proxy checks are skipped rather than guessed at.
	ProxySQLDSN string `json:"proxysql_dsn,omitempty"`
	// ExpectNodes is the membership the cluster should report. 0 means "as many
	// as are listed here".
	ExpectNodes int `json:"expect_nodes,omitempty"`
	// Backup declares how to ask this cluster when its last backup finished.
	// Optional: without it the check is skipped rather than guessed at.
	Backup *Backup `json:"backup,omitempty"`
}

// Backup is a query the operator writes and two thresholds (GD-14).
//
// This tool cannot see a dump on disk or an off-site destination — it speaks
// read-only SQL to the nodes. What it can do is grade a query against whatever
// table the backups already record into, which is why the query is declared
// here instead of a schema being invented. Both halves of the question fit in
// it: "the last backup that finished" and "the last one that reached the other
// building" are the same SELECT with a different WHERE.
//
// The query is checked at load against the same rule the query gate enforces
// at run time. A configuration file cannot smuggle a write past a tool whose
// whole promise is that it does not write.
type Backup struct {
	Query string `json:"query"`
	// Warn and Bad are durations as Go writes them — "26h", "2h30m". The
	// defaults suit a daily backup with slack: a run that is a couple of hours
	// late is not an incident, and one that missed a whole day is.
	Warn string `json:"warn_after,omitempty"`
	Bad  string `json:"bad_after,omitempty"`

	warn, bad time.Duration
}

// WarnAfter and BadAfter are the parsed thresholds, defaulted.
func (b *Backup) WarnAfter() time.Duration { return b.warn }
func (b *Backup) BadAfter() time.Duration  { return b.bad }

// File is the whole configuration.
type File struct {
	Clusters map[string]Cluster `json:"clusters"`
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load reads and validates a configuration file, expanding ${ENV_VAR} in every
// DSN.
//
// An unset variable is an error rather than an empty string: expanding
// ${DB_PASS} to nothing produces a DSN that fails to authenticate, and
// "access denied" is a much worse way to learn that an environment variable is
// missing.
func Load(path string) (File, error) {
	var f File
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Clusters) == 0 {
		return f, fmt.Errorf("%s: no cluster defined", path)
	}
	for name, c := range f.Clusters {
		if len(c.Nodes) == 0 {
			return f, fmt.Errorf("%s: cluster %q has no node", path, name)
		}
		seen := map[string]bool{}
		for i, n := range c.Nodes {
			if n.Name == "" {
				return f, fmt.Errorf("%s: cluster %q node %d has no name", path, name, i+1)
			}
			if seen[n.Name] {
				return f, fmt.Errorf("%s: cluster %q has two nodes called %q", path, name, n.Name)
			}
			seen[n.Name] = true
			if n.DSN == "" {
				return f, fmt.Errorf("%s: node %q has no dsn", n.Name, n.Name)
			}
			expanded, err := expand(n.DSN)
			if err != nil {
				return f, fmt.Errorf("%s: node %q: %w", path, n.Name, err)
			}
			c.Nodes[i].DSN = expanded
		}
		if c.Backup != nil {
			if err := c.Backup.validate(); err != nil {
				return f, fmt.Errorf("%s: cluster %q backup: %w", path, name, err)
			}
		}
		if c.ProxySQLDSN != "" {
			expanded, err := expand(c.ProxySQLDSN)
			if err != nil {
				return f, fmt.Errorf("%s: cluster %q proxysql_dsn: %w", path, name, err)
			}
			c.ProxySQLDSN = expanded
		}
		f.Clusters[name] = c
	}
	return f, nil
}

const (
	defaultBackupWarn = 26 * time.Hour
	defaultBackupBad  = 50 * time.Hour
)

func (b *Backup) validate() error {
	if strings.TrimSpace(b.Query) == "" {
		return fmt.Errorf("no query: give the SELECT that returns when the last backup finished")
	}
	// The same rule cluster.Query enforces at run time, applied where the
	// mistake is made. A config that cannot write is easier to trust than a
	// config that is stopped later.
	q := strings.TrimSpace(b.Query)
	if !cluster.ReadOnly(q) || !strings.EqualFold(strings.Fields(q)[0], "SELECT") {
		return fmt.Errorf("the query has to be a SELECT that returns when the last backup finished, got %q", firstWords(q))
	}
	// One statement. The driver disables multi-statement by default, but a DSN
	// can turn it on, and a configuration file must not be a way to send a
	// second statement past a tool whose whole promise is that it does not
	// write. A semicolon inside a string literal is refused too: being strict
	// here costs somebody a rewritten WHERE, and being loose costs the promise.
	if strings.Contains(strings.TrimSuffix(q, ";"), ";") {
		return fmt.Errorf("the query has to be a single statement, with no %q in it", ";")
	}
	b.warn, b.bad = defaultBackupWarn, defaultBackupBad
	if b.Warn != "" {
		d, err := time.ParseDuration(b.Warn)
		if err != nil {
			return fmt.Errorf("warn_after %q: %w", b.Warn, err)
		}
		b.warn = d
		// A bad_after that was not given follows the warning rather than
		// staying at a default that might now be lower than it.
		if b.Bad == "" && b.bad < b.warn {
			b.bad = 2 * b.warn
		}
	}
	if b.Bad != "" {
		d, err := time.ParseDuration(b.Bad)
		if err != nil {
			return fmt.Errorf("bad_after %q: %w", b.Bad, err)
		}
		b.bad = d
	}
	if b.warn <= 0 || b.bad <= 0 {
		return fmt.Errorf("thresholds have to be positive, got warn_after %s and bad_after %s", b.warn, b.bad)
	}
	if b.bad < b.warn {
		return fmt.Errorf("bad_after (%s) is shorter than warn_after (%s): nothing would ever be graded WARN", b.bad, b.warn)
	}
	return nil
}

func firstWords(q string) string {
	f := strings.Fields(q)
	if len(f) > 4 {
		f = f[:4]
	}
	return strings.Join(f, " ")
}

func expand(s string) (string, error) {
	var missing []string
	out := envRef.ReplaceAllStringFunc(s, func(m string) string {
		name := envRef.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return m
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("environment variable(s) not set: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// Names is the cluster names, sorted, so a run over "all" is deterministic.
func (f File) Names() []string {
	out := make([]string, 0, len(f.Clusters))
	for n := range f.Clusters {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
