package main

import (
	"strings"
	"testing"
)

func TestNodeFlagWantsNameEqualsDSN(t *testing.T) {
	var n nodeFlag
	if err := n.Set("sg-01=audit:pw@tcp(10.11.1.5:3306)/"); err != nil {
		t.Fatal(err)
	}
	if len(n) != 1 || n[0].Name != "sg-01" || !strings.HasPrefix(n[0].DSN, "audit:") {
		t.Fatalf("got %+v", n)
	}
	// A DSN contains "=" in its parameters, so the split has to be on the first
	// one only: name=user:pw@tcp(h:3306)/?timeout=5s must survive.
	if err := n.Set("cl-02=audit:pw@tcp(h:3306)/?timeout=5s"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(n[1].DSN, "?timeout=5s") {
		t.Fatalf("the DSN was truncated: %q", n[1].DSN)
	}
	for _, bad := range []string{"", "no-equals-sign", "=dsn-only", "name-only="} {
		if err := n.Set(bad); err == nil {
			t.Fatalf("Set(%q) must fail", bad)
		}
	}
}

func TestValidStatus(t *testing.T) {
	for _, ok := range []string{"", "OK", "WARN", "BAD", "ERROR"} {
		if err := validStatus(ok); err != nil {
			t.Fatalf("validStatus(%q) = %v", ok, err)
		}
	}
	if err := validStatus("bad"); err == nil {
		t.Fatal("statuses are upper case; a silently accepted lower-case one filters nothing")
	}
}
