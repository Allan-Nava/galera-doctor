package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clusters.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadExpandsEnvironmentReferences(t *testing.T) {
	t.Setenv("GD_TEST_PASS", "s3cret")
	path := write(t, `{"clusters":{"compress":{"nodes":[
	  {"name":"sg-01","dsn":"audit:${GD_TEST_PASS}@tcp(10.11.1.5:3306)/"}],
	  "expect_nodes":3}}}`)
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Clusters["compress"].Nodes[0].DSN; !strings.Contains(got, "s3cret") {
		t.Fatalf("dsn = %q", got)
	}
	if f.Clusters["compress"].ExpectNodes != 3 {
		t.Fatal("expect_nodes lost")
	}
}

// Expanding a missing variable to an empty string produces "access denied",
// which is a terrible way to learn that an environment variable is unset.
func TestUnsetEnvironmentVariableIsAnError(t *testing.T) {
	path := write(t, `{"clusters":{"c":{"nodes":[{"name":"a","dsn":"u:${GD_NOT_SET_ANYWHERE}@tcp(h:3306)/"}]}}}`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "GD_NOT_SET_ANYWHERE") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]string{
		"no cluster":     `{"clusters":{}}`,
		"no node":        `{"clusters":{"c":{"nodes":[]}}}`,
		"no name":        `{"clusters":{"c":{"nodes":[{"dsn":"u@tcp(h:3306)/"}]}}}`,
		"no dsn":         `{"clusters":{"c":{"nodes":[{"name":"a"}]}}}`,
		"duplicate name": `{"clusters":{"c":{"nodes":[{"name":"a","dsn":"x"},{"name":"a","dsn":"y"}]}}}`,
		"typo in a key":  `{"clusters":{"c":{"noeds":[{"name":"a","dsn":"x"}]}}}`,
	}
	for what, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Fatalf("%s must be rejected", what)
		}
	}
}

func TestNamesAreSorted(t *testing.T) {
	path := write(t, `{"clusters":{
	  "sslazio":{"nodes":[{"name":"a","dsn":"x"}]},
	  "compress":{"nodes":[{"name":"b","dsn":"y"}]}}}`)
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := f.Names()
	if got[0] != "compress" || got[1] != "sslazio" {
		t.Fatalf("names = %v, want sorted", got)
	}
}
