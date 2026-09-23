package main

// GD-75 — `galera-doctor checks` is a list somebody typed, describing output a
// program generates. Nothing compared the two until this file, and by then it
// had already drifted: cluster/membership, cluster/provider-version and
// node/not-galera fired in the field and were absent from the list, so an
// operator grepping the documented checks for the line in their report found
// nothing and concluded the tool was broken.
//
// The comparison cannot be made by running an audit — no fixture triggers all
// seventy checks at once, and one that did would be a second hand-written list
// with the same failure mode. It is made against the source: every check id is
// a string literal in internal/audit or internal/proxysql, and this walks them
// with go/ast rather than a grep, so a literal split over two lines or written
// with an escape still counts.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// checkShape is what a check id looks like: one lowercase word, a slash, and a
// hyphenated lowercase word. Deliberately narrow — it is the shape of the ids
// in checkRows, and anything that drifts out of it is a naming mistake this
// test should also catch.
var checkShape = regexp.MustCompile(`^[a-z][a-z0-9]*/[a-z0-9-]+$`)

// documentedChecks is the ids in checkRows, one row of which may list several
// checks that share a description. The wildcard row (proxysql/*) is a heading
// for the ids below it, not an id.
func documentedChecks(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, row := range checkRows {
		for _, id := range strings.Split(row[0], ",") {
			id = strings.TrimSpace(id)
			if id == "" || strings.HasSuffix(id, "/*") {
				continue
			}
			if !checkShape.MatchString(id) {
				t.Errorf("checkRows lists %q, which is not the shape of a check id", id)
				continue
			}
			if out[id] {
				t.Errorf("checkRows lists %q twice", id)
			}
			out[id] = true
		}
	}
	return out
}

// emittedChecks is every check-shaped string literal the audit packages
// construct.
//
// Import paths and case clauses are skipped, and both for the same reason:
// they are the two places a check-shaped string appears without being a check.
// "database/sql" is an import; "donor/desynced" is a value of
// wsrep_local_state_comment that stateComment switches on. Neither is
// something `checks` should print, and neither is something a maintainer
// should have to remember to exempt — the syntax says so.
func emittedChecks(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range []string{"internal/audit", "internal/proxysql"} {
		dir = filepath.Join(repoRoot(t), dir)
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", dir, err)
		}
		if len(pkgs) == 0 {
			t.Fatalf("no package parsed in %s — the paths in this test are stale", dir)
		}
		for _, pkg := range pkgs {
			for name, file := range pkg.Files {
				var scan func(ast.Node) bool
				scan = func(n ast.Node) bool {
					switch v := n.(type) {
					case *ast.ImportSpec:
						return false
					case *ast.CaseClause:
						// Only the case *expressions* are skipped. Skipping
						// the whole clause would drop the body with them, and
						// the body is where a switch's findings are built —
						// cluster/peers and proxysql/disagreement both live
						// there, and an earlier draft of this test reported
						// them as undocumented and then as phantom.
						for _, stmt := range v.Body {
							ast.Inspect(stmt, scan)
						}
						return false
					}
					lit, ok := n.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return true
					}
					v, err := strconv.Unquote(lit.Value)
					if err != nil || !checkShape.MatchString(v) {
						return true
					}
					if _, seen := out[v]; !seen {
						out[v] = filepath.Base(name) + ":" + strconv.Itoa(fset.Position(lit.Pos()).Line)
					}
					return true
				}
				ast.Inspect(file, scan)
			}
		}
	}
	return out
}

// repoRoot is two directories up from this file, which is where internal/
// lives. Derived from the compiler rather than from the working directory so
// the test does not depend on where `go test` was invoked.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(self)))
}

func TestEveryCheckTheAuditEmitsIsDocumented(t *testing.T) {
	documented := documentedChecks(t)
	var missing []string
	for id, where := range emittedChecks(t) {
		if !documented[id] {
			missing = append(missing, id+" ("+where+")")
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("these checks fire and `galera-doctor checks` does not list them:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

func TestEveryDocumentedCheckIsOneTheAuditEmits(t *testing.T) {
	emitted := emittedChecks(t)
	var phantom []string
	for id := range documentedChecks(t) {
		if _, ok := emitted[id]; !ok {
			phantom = append(phantom, id)
		}
	}
	sort.Strings(phantom)
	if len(phantom) != 0 {
		t.Fatalf("`galera-doctor checks` lists checks nothing can emit — renamed or removed:\n  %s",
			strings.Join(phantom, "\n  "))
	}
}

// The scan is only as good as what it finds, so it is worth asserting it found
// the shape of thing it is looking for at all: a test that parses nothing
// passes both comparisons above and proves nothing.
func TestTheScanFindsChecksInBothPackages(t *testing.T) {
	emitted := emittedChecks(t)
	if len(emitted) < 50 {
		t.Fatalf("the scan found %d check ids — it is not reading the audit source", len(emitted))
	}
	for _, want := range []string{"systables/drift", "proxysql/monitor"} {
		if _, ok := emitted[want]; !ok {
			t.Errorf("the scan missed %q", want)
		}
	}
	for _, notAChecked := range []string{"database/sql", "donor/desynced"} {
		if where, ok := emitted[notAChecked]; ok {
			t.Errorf("the scan took %q (%s) for a check id", notAChecked, where)
		}
	}
}
