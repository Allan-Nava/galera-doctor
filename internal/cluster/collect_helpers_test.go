package cluster

// GD-76 — the parts of collect.go that need no server.
//
// Most of this file is SQL and belongs to the integration test behind
// GD_TEST_DSN. These three do not: parseWhen decides what a backup timestamp
// means, placeholders builds a fragment of every IN clause the collector
// sends, and redact is the only thing standing between a password and a
// ticket. All three were reachable only through a live server, which is to
// say: not on anybody's laptop.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseWhenAcceptsEveryShapeABackupTableUses(t *testing.T) {
	want := time.Date(2026, 9, 21, 3, 15, 0, 0, time.UTC)
	for _, in := range []string{
		"2026-09-21 03:15:00",
		"2026-09-21T03:15:00Z",
		"2026-09-21T03:15:00.000Z",
	} {
		got, err := parseWhen(in)
		if err != nil {
			t.Errorf("parseWhen(%q): %v", in, err)
			continue
		}
		if !got.UTC().Equal(want) {
			t.Errorf("parseWhen(%q) = %v, want %v", in, got.UTC(), want)
		}
	}
}

func TestParseWhenReadsADateAsMidnight(t *testing.T) {
	got, err := parseWhen("2026-09-21")
	if err != nil {
		t.Fatal(err)
	}
	if !got.UTC().Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("a DATE is midnight, got %v", got.UTC())
	}
}

func TestParseWhenReadsAnEpochIncludingItsFraction(t *testing.T) {
	// A backup table that records UNIX_TIMESTAMP() gives a plain number, and
	// some of them give it with a fraction. Dropping the fraction is harmless;
	// failing to parse the value is not, because backup/freshness would then
	// report a backup it could not read rather than a backup that is old.
	got, err := parseWhen("1758424500")
	if err != nil {
		t.Fatal(err)
	}
	if got.Unix() != 1758424500 {
		t.Fatalf("epoch: got %v", got)
	}
	frac, err := parseWhen("1758424500.500")
	if err != nil {
		t.Fatal(err)
	}
	if frac.Unix() != 1758424500 || frac.Nanosecond() == 0 {
		t.Fatalf("the fraction was dropped or mangled: %v", frac)
	}
}

func TestParseWhenRefusesWhatItCannotRead(t *testing.T) {
	// An unparseable value has to be an error rather than the zero time: the
	// zero time is January of year 1, and a freshness check would grade that
	// as the oldest backup imaginable and report a catastrophe instead of
	// saying it could not read the column.
	for _, in := range []string{"", "never", "0000-00-00 00:00:00", "21/09/2026", "0", "-1"} {
		if got, err := parseWhen(in); err == nil {
			t.Errorf("parseWhen(%q) = %v, want an error", in, got)
		}
	}
}

func TestPlaceholdersMatchesTheArgumentCount(t *testing.T) {
	for n, want := range map[int]string{0: "", 1: "?", 2: "?,?", 5: "?,?,?,?,?"} {
		if got := placeholders(n); got != want {
			t.Errorf("placeholders(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestErrStringIsEmptyForNoError(t *testing.T) {
	// A Snapshot.Err of "" is what OK() reads as "the node was read", so
	// turning a nil error into anything else would mark every healthy node as
	// unread.
	if got := errString(nil); got != "" {
		t.Fatalf("errString(nil) = %q, want empty", got)
	}
	if got := errString(errors.New("boom")); got != "boom" {
		t.Fatalf("errString = %q", got)
	}
}

func TestRedactRemovesTheCredentialsHoweverTheyAreQuoted(t *testing.T) {
	// The driver quotes the DSN it was given, but not always whole: some
	// errors carry only the user:password prefix, and one carries the bare
	// password. All three have to come out, because the output of this tool
	// is pasted into tickets.
	dsn := "audit:sup3rs3cret@tcp(10.11.1.5:3306)/"
	for name, msg := range map[string]string{
		"the whole dsn":   `dial error for dsn "` + dsn + `": timeout`,
		"the credentials": `access denied for audit:sup3rs3cret`,
		"the password":    `handshake failed (password sup3rs3cret)`,
	} {
		got := redact(msg, dsn)
		if strings.Contains(got, "sup3rs3cret") {
			t.Errorf("%s: the password survived redaction: %q", name, got)
		}
	}
}

func TestRedactLeavesAMessageAloneWhenThereIsNoDSN(t *testing.T) {
	// An empty DSN must not turn into a match that blanks the whole message:
	// strings.ReplaceAll with an empty needle inserts the replacement between
	// every character.
	msg := "context deadline exceeded"
	if got := redact(msg, ""); got != msg {
		t.Fatalf("redact with no dsn rewrote the message: %q", got)
	}
}
