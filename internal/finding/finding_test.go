package finding

import "testing"

func TestSeverityOrder(t *testing.T) {
	if !AtLeast(ERROR, BAD) || !AtLeast(BAD, WARN) || !AtLeast(WARN, OK) {
		t.Fatal("severity order must be OK < WARN < BAD < ERROR")
	}
	if AtLeast(OK, WARN) {
		t.Fatal("OK must not satisfy a WARN threshold")
	}
	// An unread node outranks a bad one: a caller filtering on BAD has to see
	// the nodes the audit could not reach, because every cluster-wide statement
	// was made without them.
	if Severity(ERROR) <= Severity(BAD) {
		t.Fatal("ERROR must sort above BAD")
	}
}

func TestWorstAndSummarize(t *testing.T) {
	fs := []Finding{{Status: OK}, {Status: WARN}, {Status: BAD}}
	if got := Worst(fs); got != BAD {
		t.Fatalf("Worst = %s, want BAD", got)
	}
	if got := Worst(nil); got != OK {
		t.Fatalf("Worst(nil) = %s, want OK — no findings is not a failure", got)
	}
	sum := Summarize(fs)
	if sum[OK] != 1 || sum[WARN] != 1 || sum[BAD] != 1 || sum[ERROR] != 0 {
		t.Fatalf("Summarize = %v", sum)
	}
}

func TestSortWorstFirstGroupsByCheck(t *testing.T) {
	fs := []Finding{
		{Check: "node/state", Target: "sg-01", Status: OK},
		{Check: "flow/paused", Target: "cl-02", Status: BAD},
		{Check: "cluster/uuid", Target: "cluster", Status: BAD},
	}
	SortWorstFirst(fs)
	if fs[0].Check != "cluster/uuid" {
		t.Fatalf("worst first, then check, then target: got %+v", fs[0])
	}
	if fs[2].Status != OK {
		t.Fatalf("OK must sort last: got %+v", fs[2])
	}
}
