package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := `
# comment
PLAIN=value
export EXPORTED=yes
DOUBLE="has spaces"
SINGLE='x=y'
URL=postgres://u:p@host:5432/db?sslmode=disable
ALREADY=from-file
EMPTY=
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALREADY", "from-env")
	for _, k := range []string{"PLAIN", "EXPORTED", "DOUBLE", "SINGLE", "URL", "EMPTY"} {
		t.Setenv(k, "") // registers cleanup
		os.Unsetenv(k)
	}

	found, err := LoadDotEnv(path)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	want := map[string]string{
		"PLAIN": "value", "EXPORTED": "yes", "DOUBLE": "has spaces", "SINGLE": "x=y",
		"URL": "postgres://u:p@host:5432/db?sslmode=disable", "ALREADY": "from-env", "EMPTY": "",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	if found, err := LoadDotEnv(filepath.Join(t.TempDir(), "missing")); found || err != nil {
		t.Errorf("missing file: found=%v err=%v", found, err)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	os.WriteFile(bad, []byte("NOEQUALS\n"), 0o600)
	if _, err := LoadDotEnv(bad); err == nil {
		t.Error("malformed line accepted")
	}
}
