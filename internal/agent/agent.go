// Package agent applies up/down commands to the OpenResty instance it runs
// next to: it writes or removes config files, validates the whole
// configuration and reloads, rolling the files back if either step fails.
//
// Each up writes <deployment id>.conf whose first line names the config it
// belongs to, and removes the files of earlier deployments of that config.
// A down removes every file of the config.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/mq"
	"github.com/abu-maryam-rumaisha/nlp-scheduler/scheduler/internal/nginx"
)

// markerPrefix starts the first line of every file the agent writes, tying
// it to its config name.
const markerPrefix = "# config: "

var deploymentFileRe = regexp.MustCompile(`^([0-9]+)\.conf$`)

type Applier struct {
	ConfDir    string   // directory included by nginx.conf, e.g. conf.d
	TestCmd    []string // e.g. openresty -t
	ReloadCmd  []string // e.g. openresty -s reload
	CmdTimeout time.Duration
}

func (a *Applier) Apply(ctx context.Context, cmd mq.Command) error {
	if !nginx.ResourceNameRe.MatchString(cmd.ConfigName) {
		return fmt.Errorf("invalid config name %q", cmd.ConfigName)
	}
	switch cmd.Action {
	case mq.ActionUp:
		if cmd.DeploymentID <= 0 {
			return fmt.Errorf("invalid deployment id %d", cmd.DeploymentID)
		}
		return a.up(ctx, cmd.DeploymentID, cmd.ConfigName, cmd.Config)
	case mq.ActionDown:
		return a.down(ctx, cmd.ConfigName)
	default:
		return fmt.Errorf("unknown action %q", cmd.Action)
	}
}

// configFile is a file on disk that belongs to a config.
type configFile struct {
	path         string
	deploymentID int64 // 0 for a legacy <config name>.conf
}

// change records a file's content before it was touched, for rollback.
type change struct {
	path    string
	prev    []byte
	existed bool
}

func (a *Applier) up(ctx context.Context, deploymentID int64, configName, content string) error {
	files, err := a.configFiles(configName)
	if err != nil {
		return err
	}
	path := a.deploymentPath(deploymentID)
	data := []byte(markerPrefix + configName + "\n" + content)
	var stale []string
	for _, f := range files {
		if f.deploymentID > deploymentID {
			return fmt.Errorf("superseded by deployment %d", f.deploymentID)
		}
		if f.path != path {
			stale = append(stale, f.path)
		}
	}

	prev, existed, err := readIfExists(path)
	if err != nil {
		return err
	}
	if existed && bytes.Equal(prev, data) && len(stale) == 0 {
		return nil // already up to date; skip the reload
	}
	changes := []change{{path, prev, existed}}
	if err := writeAtomic(path, data); err != nil {
		return err
	}
	for _, p := range stale {
		c, err := remove(p)
		if err != nil {
			return errors.Join(err, rollbackErr(restoreAll(changes)))
		}
		changes = append(changes, c)
	}
	return a.activate(ctx, func() error { return restoreAll(changes) })
}

func (a *Applier) down(ctx context.Context, configName string) error {
	files, err := a.configFiles(configName)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil // already down
	}
	var changes []change
	for _, f := range files {
		c, err := remove(f.path)
		if err != nil {
			return errors.Join(err, rollbackErr(restoreAll(changes)))
		}
		changes = append(changes, c)
	}
	return a.activate(ctx, func() error { return restoreAll(changes) })
}

func (a *Applier) deploymentPath(id int64) string {
	return filepath.Join(a.ConfDir, strconv.FormatInt(id, 10)+".conf")
}

// configFiles returns the files in ConfDir that belong to configName: the
// <deployment id>.conf files marked with it, plus a legacy <config name>.conf
// written by agents that named files after the config.
func (a *Applier) configFiles(configName string) ([]configFile, error) {
	entries, err := os.ReadDir(a.ConfDir)
	if err != nil {
		return nil, err
	}
	var files []configFile
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(a.ConfDir, e.Name())
		if e.Name() == configName+".conf" {
			files = append(files, configFile{path: path})
			continue
		}
		m := deploymentFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		name, err := readMarker(path)
		if err != nil {
			return nil, err
		}
		if name == configName {
			files = append(files, configFile{path: path, deploymentID: id})
		}
	}
	return files, nil
}

// readMarker returns the config name on a file's first line, or "" if the
// file has no marker.
func readMarker(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && line == "" {
		return "", nil
	}
	name, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), markerPrefix)
	if !ok {
		return "", nil
	}
	return name, nil
}

// activate tests and reloads the configuration, calling rollback if either
// fails.
func (a *Applier) activate(ctx context.Context, rollback func() error) error {
	if err := a.run(ctx, a.TestCmd); err != nil {
		return errors.Join(fmt.Errorf("config test failed: %w", err), rollbackErr(rollback()))
	}
	if err := a.run(ctx, a.ReloadCmd); err != nil {
		return errors.Join(fmt.Errorf("reload failed: %w", err), rollbackErr(rollback()))
	}
	return nil
}

func rollbackErr(err error) error {
	if err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}
	return nil
}

func (a *Applier) run(ctx context.Context, argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, a.CmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func readIfExists(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

// remove deletes path, returning what it held for rollback.
func remove(path string) (change, error) {
	prev, existed, err := readIfExists(path)
	if err != nil || !existed {
		return change{path, nil, false}, err
	}
	if err := os.Remove(path); err != nil {
		return change{}, err
	}
	return change{path, prev, true}, nil
}

// restoreAll puts every changed file back as it was, newest change first.
func restoreAll(changes []change) error {
	var errs []error
	for i := len(changes) - 1; i >= 0; i-- {
		errs = append(errs, restore(changes[i]))
	}
	return errors.Join(errs...)
}

func restore(c change) error {
	if !c.existed {
		err := os.Remove(c.path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return writeAtomic(c.path, c.prev)
}

// writeAtomic writes via a temp file in the same directory. The temp name
// does not end in .conf, so an `include *.conf` never picks it up.
func writeAtomic(path string, data []byte) error {
	dir, base := filepath.Split(path)
	f, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
