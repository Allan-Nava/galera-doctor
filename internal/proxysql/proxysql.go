// Package proxysql reads a ProxySQL admin interface and compares what the
// proxy believes about the cluster with what the cluster says about itself.
//
// The two disagreeing is the interesting state, and it is invisible from either
// side alone: Galera reports a node as Synced while the proxy has it shunned,
// or the proxy is happily sending writes to a node that is still joining. A
// dashboard on either one looks fine.
//
// One rule is baked in: the **offline hostgroup is managed by ProxySQL's own
// Galera monitor** — the notorious 999 in most deployments — and its contents
// are never a finding. Nodes are moved in and out of it automatically, and a
// check that flags it produces a permanent false positive plus, eventually, a
// cleanup that fights the monitor.
package proxysql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/cluster"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
)

// Server is one row of runtime_mysql_servers.
type Server struct {
	Hostgroup int    `json:"hostgroup"`
	Hostname  string `json:"hostname"`
	Port      int    `json:"port"`
	Status    string `json:"status"`
	Weight    int    `json:"weight"`
	Comment   string `json:"comment,omitempty"`
}

// HostgroupSet is one row of runtime_mysql_galera_hostgroups: the mapping the
// monitor is driving.
type HostgroupSet struct {
	Writer       int `json:"writer"`
	BackupWriter int `json:"backup_writer"`
	Reader       int `json:"reader"`
	Offline      int `json:"offline"`
	Active       int `json:"active"`
}

// Snapshot is one read of a ProxySQL admin interface.
type Snapshot struct {
	At         time.Time      `json:"at"`
	Servers    []Server       `json:"servers,omitempty"`
	Hostgroups []HostgroupSet `json:"hostgroups,omitempty"`
	// MonitorEnabled is mysql-monitor_enabled. A pointer because nil means
	// "not read" — an unread variable is not a variable set to false, and the
	// difference decides whether proxysql/monitor says anything at all.
	MonitorEnabled *bool  `json:"monitor_enabled,omitempty"`
	Err            string `json:"error,omitempty"`
}

// OK reports whether the proxy was read.
func (s Snapshot) OK() bool { return s.Err == "" }

// Collect reads the admin interface. Like the cluster collector it returns the
// failure inside the snapshot: an unreadable proxy is a finding, not a reason
// to abandon the audit.
func Collect(ctx context.Context, dsn string, timeout time.Duration) Snapshot {
	snap := Snapshot{At: time.Now()}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		snap.Err = err.Error()
		return snap
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		snap.Err = "admin interface not reachable"
		return snap
	}

	rows, err := cluster.Query(ctx, db, `SELECT hostgroup_id, hostname, port, status, weight, comment
	                                       FROM runtime_mysql_servers ORDER BY hostgroup_id, hostname`)
	if err != nil {
		snap.Err = err.Error()
		return snap
	}
	for rows.Next() {
		var s Server
		var comment sql.NullString
		if err := rows.Scan(&s.Hostgroup, &s.Hostname, &s.Port, &s.Status, &s.Weight, &comment); err != nil {
			rows.Close()
			snap.Err = err.Error()
			return snap
		}
		s.Comment = comment.String
		snap.Servers = append(snap.Servers, s)
	}
	rows.Close()

	// A deployment without the Galera monitor has no such table; that is a
	// configuration choice, not an error, so the mapping is simply unknown and
	// the checks that need it are skipped.
	hgs, err := cluster.Query(ctx, db, `SELECT writer_hostgroup, backup_writer_hostgroup, reader_hostgroup,
	                                           offline_hostgroup, active
	                                      FROM runtime_mysql_galera_hostgroups`)
	if err == nil {
		for hgs.Next() {
			var h HostgroupSet
			if err := hgs.Scan(&h.Writer, &h.BackupWriter, &h.Reader, &h.Offline, &h.Active); err == nil {
				snap.Hostgroups = append(snap.Hostgroups, h)
			}
		}
		hgs.Close()
	}

	// Whether the monitor that drives those hostgroups is running at all
	// (GD-42). A proxy that does not answer this leaves it nil: not read.
	if vars, err := cluster.Query(ctx, db, `SELECT variable_value FROM runtime_global_variables
	                                         WHERE variable_name = 'mysql-monitor_enabled'`); err == nil {
		for vars.Next() {
			var v string
			if err := vars.Scan(&v); err == nil {
				on := strings.EqualFold(strings.TrimSpace(v), "true") || strings.TrimSpace(v) == "1"
				snap.MonitorEnabled = &on
			}
		}
		vars.Close()
	}
	return snap
}

// MonitorManaged reports whether a hostgroup is one the Galera monitor moves
// nodes into on its own — the offline hostgroups. Its contents are never
// graded.
func (s Snapshot) MonitorManaged(hostgroup int) bool {
	for _, h := range s.Hostgroups {
		if h.Offline == hostgroup {
			return true
		}
	}
	return false
}

// Audit compares the proxy's view with the cluster's.
func Audit(snap Snapshot, snaps []cluster.Snapshot) []finding.Finding {
	if !snap.OK() {
		return []finding.Finding{{
			Check: "proxysql/read", Target: "proxysql", Status: finding.ERROR,
			Message: "admin interface not read: " + snap.Err,
			Hint:    "no statement about the traffic path was made — the cluster findings still stand",
		}}
	}
	var out []finding.Finding
	out = append(out, monitorRunning(snap)...)

	// Which addresses does the proxy know about, outside the monitor's own
	// offline hostgroup?
	known := map[string]bool{}
	for _, srv := range snap.Servers {
		if snap.MonitorManaged(srv.Hostgroup) {
			continue
		}
		known[srv.Hostname] = true
	}

	for _, node := range snaps {
		if !node.OK() {
			continue
		}
		var match []Server
		for _, srv := range snap.Servers {
			if snap.MonitorManaged(srv.Hostgroup) {
				continue
			}
			for _, addr := range node.Addresses() {
				if strings.EqualFold(addr, srv.Hostname) {
					match = append(match, srv)
					break
				}
			}
		}
		if len(match) == 0 {
			out = append(out, finding.Finding{
				Check: "proxysql/missing", Target: node.Node, Status: finding.BAD,
				Message: "the node is in the cluster and not in any serving hostgroup",
				Hint: "no traffic will ever reach it: capacity you are paying for and not using, and a failover target that cannot take over. " +
					"Tried " + strings.Join(node.Addresses(), ", "),
			})
			continue
		}
		synced := false
		if c, ok := node.Get("wsrep_local_state_comment"); ok {
			synced = strings.EqualFold(strings.TrimSpace(c), "synced")
		}
		for _, srv := range match {
			online := strings.EqualFold(srv.Status, "ONLINE")
			switch {
			case online && !synced:
				comment, _ := node.Get("wsrep_local_state_comment")
				out = append(out, finding.Finding{
					Check: "proxysql/disagreement", Target: node.Node, Status: finding.BAD,
					Message: fmt.Sprintf("ONLINE in hostgroup %d while the node reports %s", srv.Hostgroup, orUnknown(comment)),
					Hint:    "queries are being sent to a node that is not synced: the monitor has not caught up, or it is not running",
				})
			case !online && synced:
				out = append(out, finding.Finding{
					Check: "proxysql/disagreement", Target: node.Node, Status: finding.WARN,
					Message: fmt.Sprintf("%s in hostgroup %d while the node reports Synced", srv.Status, srv.Hostgroup),
					Hint:    "the proxy is refusing a healthy node — a shun that never cleared, or a monitor login that fails",
				})
			}
		}
	}

	// A hostgroup with nothing serving in it is an outage in waiting, and it is
	// the one thing a per-node view cannot see.
	byGroup := map[int][]Server{}
	for _, srv := range snap.Servers {
		byGroup[srv.Hostgroup] = append(byGroup[srv.Hostgroup], srv)
	}
	groups := make([]int, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	sort.Ints(groups)
	for _, g := range groups {
		if snap.MonitorManaged(g) {
			continue
		}
		var online int
		for _, srv := range byGroup[g] {
			if strings.EqualFold(srv.Status, "ONLINE") {
				online++
			}
		}
		if online == 0 {
			out = append(out, finding.Finding{
				Check: "proxysql/hostgroup", Target: fmt.Sprintf("hostgroup %d", g), Status: finding.BAD,
				Message: fmt.Sprintf("%d server(s) configured, none ONLINE", len(byGroup[g])),
				Value:   finding.Num(0), Unit: "servers",
				Hint: "every query routed to this hostgroup fails",
			})
		}
	}
	// Say the offline hostgroup was seen and deliberately not graded, so nobody
	// goes looking for the check that is "missing".
	for _, h := range snap.Hostgroups {
		out = append(out, finding.Finding{
			Check: "proxysql/mapping", Target: fmt.Sprintf("hostgroup %d/%d/%d", h.Writer, h.Reader, h.Offline), Status: finding.OK,
			Message: fmt.Sprintf("writer %d, backup writer %d, reader %d, offline %d (monitor-managed, not graded)",
				h.Writer, h.BackupWriter, h.Reader, h.Offline),
			Hint: "the offline hostgroup is where ProxySQL's Galera monitor parks nodes on its own: its contents are never a finding",
		})
	}
	return out
}

// monitorRunning reports a proxy whose Galera monitor has stopped (GD-42).
//
// Every other check in this package compares the proxy's hostgroups with the
// cluster's state. The monitor is what keeps those hostgroups current — it is
// the thing that moves a desynced node out of the writer hostgroup — so with it
// off, the comparison is being made against a photograph. It can agree
// perfectly with a cluster that moved hours ago, which is the failure this
// package exists to catch, one level up.
//
// A deployment with no Galera hostgroup table at all is a configuration choice
// rather than a stopped monitor, and an unread variable is not a variable set
// to false: both stay quiet.
func monitorRunning(snap Snapshot) []finding.Finding {
	var out []finding.Finding

	if snap.MonitorEnabled != nil && !*snap.MonitorEnabled {
		out = append(out, finding.Finding{
			Check: "proxysql/monitor", Target: "proxysql", Status: finding.BAD,
			Message: "mysql-monitor_enabled is false: nothing is updating the hostgroups",
			Hint: "every other proxysql/* finding below is a photograph, not live — the monitor is what moves a desynced node out of the writer hostgroup, " +
				"so the proxy can agree with a cluster that moved hours ago",
		})
	}

	for _, h := range snap.Hostgroups {
		if h.Active != 0 {
			continue
		}
		out = append(out, finding.Finding{
			Check: "proxysql/monitor", Target: fmt.Sprintf("hostgroup %d", h.Writer), Status: finding.BAD,
			Message: fmt.Sprintf("the Galera hostgroup set for writer %d is inactive (active=0)", h.Writer),
			Hint: "the monitor does not drive an inactive set, so nodes are never moved between these hostgroups: " +
				"the mapping is a photograph and not live, however well it happens to match right now",
		})
	}
	return out
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "an unknown state"
	}
	return s
}
