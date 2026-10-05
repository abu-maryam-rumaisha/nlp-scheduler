package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/mq"
)

func newApplier(t *testing.T, testCmd []string) (*Applier, string) {
	dir := t.TempDir()
	return &Applier{ConfDir: dir, TestCmd: testCmd, ReloadCmd: []string{"true"}, CmdTimeout: 5 * time.Second}, dir
}

func up(id int64, name, config string) mq.Command {
	return mq.Command{DeploymentID: id, Action: mq.ActionUp, ConfigName: name, Config: config}
}

func down(id int64, name string) mq.Command {
	return mq.Command{DeploymentID: id, Action: mq.ActionDown, ConfigName: name}
}

func readConf(t *testing.T, dir, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestUpAndDown(t *testing.T) {
	a, dir := newApplier(t, []string{"true"})
	ctx := context.Background()

	if err := a.Apply(ctx, up(1, "site", "v1")); err != nil {
		t.Fatal(err)
	}
	if got := readConf(t, dir, "1.conf"); got != "# config: site\nv1" {
		t.Fatalf("got %q", got)
	}
	if err := a.Apply(ctx, up(2, "other", "o1")); err != nil {
		t.Fatal(err)
	}
	// A new up replaces the earlier deployment's file of the same config only.
	if err := a.Apply(ctx, up(3, "site", "v2")); err != nil {
		t.Fatal(err)
	}
	if got := listDir(t, dir); !slices.Equal(got, []string{"2.conf", "3.conf"}) {
		t.Fatalf("files after redeploy: %v", got)
	}
	// Redelivery of the same command is a no-op.
	if err := a.Apply(ctx, up(3, "site", "v2")); err != nil {
		t.Fatal(err)
	}
	// An older up arriving late does not clobber the newer one.
	if err := a.Apply(ctx, up(1, "site", "v1")); err == nil {
		t.Fatal("expected stale up to be rejected")
	}
	if got := readConf(t, dir, "3.conf"); got != "# config: site\nv2" {
		t.Fatalf("got %q", got)
	}

	if err := a.Apply(ctx, down(4, "site")); err != nil {
		t.Fatal(err)
	}
	if got := listDir(t, dir); !slices.Equal(got, []string{"2.conf"}) {
		t.Fatalf("files after down: %v", got)
	}
	// Down is idempotent.
	if err := a.Apply(ctx, down(5, "site")); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyFileReplaced(t *testing.T) {
	a, dir := newApplier(t, []string{"true"})
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "site.conf"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(ctx, up(9, "site", "v1")); err != nil {
		t.Fatal(err)
	}
	if got := listDir(t, dir); !slices.Equal(got, []string{"9.conf"}) {
		t.Fatalf("files: %v", got)
	}
}

func TestFailedTestRollsBack(t *testing.T) {
	a, dir := newApplier(t, []string{"true"})
	ctx := context.Background()
	if err := a.Apply(ctx, up(1, "site", "good")); err != nil {
		t.Fatal(err)
	}

	a.TestCmd = []string{"sh", "-c", "echo 'unknown directive' >&2; exit 1"}
	if err := a.Apply(ctx, up(2, "site", "bad")); err == nil {
		t.Fatal("expected failure")
	}
	if got := listDir(t, dir); !slices.Equal(got, []string{"1.conf"}) {
		t.Fatalf("not rolled back, files: %v", got)
	}
	if got := readConf(t, dir, "1.conf"); got != "# config: site\ngood" {
		t.Fatalf("not rolled back, got %q", got)
	}

	if err := a.Apply(ctx, up(3, "fresh", "bad")); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "3.conf")); !os.IsNotExist(err) {
		t.Fatal("new file left behind after failed test")
	}

	if err := a.Apply(ctx, down(4, "site")); err == nil {
		t.Fatal("expected failure")
	}
	if got := readConf(t, dir, "1.conf"); got != "# config: site\ngood" {
		t.Fatalf("down not rolled back, got %q", got)
	}
}

func TestRejectsPathTraversal(t *testing.T) {
	a, _ := newApplier(t, []string{"true"})
	err := a.Apply(context.Background(), up(1, "../etc/passwd", "x"))
	if err == nil {
		t.Fatal("expected rejection")
	}
}

func TestHandle(t *testing.T) {
	a, dir := newApplier(t, []string{"true"})
	cfg := Config{Instance: "edge-1", Service: "edge", Applier: a}

	res := handle(context.Background(), cfg, mq.Command{DeploymentID: 7, Instance: "edge-1", Action: mq.ActionUp, ConfigName: "site", Config: "x"})
	if !res.Success || res.DeploymentID != 7 || res.Instance != "edge-1" || res.Service != "edge" || res.FinishedAt.IsZero() {
		t.Fatalf("unexpected result %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "7.conf")); err != nil {
		t.Fatal(err)
	}

	res = handle(context.Background(), cfg, mq.Command{DeploymentID: 8, Instance: "edge-2", Action: mq.ActionUp, ConfigName: "other", Config: "x"})
	if res.Success || res.Error == "" {
		t.Fatalf("misrouted command applied: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "8.conf")); !os.IsNotExist(err) {
		t.Fatal("misrouted command wrote a file")
	}
}
