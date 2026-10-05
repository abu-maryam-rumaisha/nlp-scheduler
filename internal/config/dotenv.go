package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
)

// LoadEnvFile loads .env, or the file named by ENV_FILE, if it exists.
func LoadEnvFile() error {
	path := String("ENV_FILE", ".env")
	found, err := LoadDotEnv(path)
	if found && err == nil {
		slog.Info("loaded env file", "path", path)
	}
	return err
}

// LoadDotEnv reads KEY=VALUE lines from path into the environment. Variables
// that are already set win, so the real environment can override the file.
// A missing file is not an error. It reports whether the file was found.
//
// Supported: blank lines, `# comments`, an optional `export ` prefix, and
// values wrapped in single or double quotes.
func LoadDotEnv(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return true, fmt.Errorf("%s:%d: expected KEY=VALUE", path, n)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return true, err
		}
	}
	return true, sc.Err()
}
