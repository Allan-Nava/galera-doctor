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
}

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
