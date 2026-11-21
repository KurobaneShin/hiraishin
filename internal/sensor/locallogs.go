package sensor

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxLogLine = 4 << 20

func ClaudeTokens(home string, since time.Time) (int64, error) {
	var total int64
	err := walkRecent(filepath.Join(home, ".claude", "projects"), since, func(path string) bool { return strings.HasSuffix(path, ".jsonl") }, func(line []byte) {
		var rec struct {
			Timestamp time.Time `json:"timestamp"`
			Message   struct {
				Usage struct {
					Input         int64 `json:"input_tokens"`
					Output        int64 `json:"output_tokens"`
					CacheCreation int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Timestamp.Before(since) {
			return
		}
		u := rec.Message.Usage
		total += u.Input + u.Output + u.CacheCreation
	})
	return total, err
}

func AgyTurns(home string, since time.Time) (int64, error) {
	var n int64
	match := func(path string) bool { return filepath.Base(path) == "transcript.jsonl" }
	err := walkRecent(filepath.Join(home, ".gemini", "antigravity-cli", "brain"), since, match, func([]byte) { n++ })
	return n, err
}

func walkRecent(root string, since time.Time, match func(string) bool, each func([]byte)) error {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !match(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(since) {
			return nil
		}
		return scanLines(path, each)
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func scanLines(path string, each func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxLogLine)
	for sc.Scan() {
		each(sc.Bytes())
	}
	return sc.Err()
}
