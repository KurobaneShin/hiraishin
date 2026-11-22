package stacktest

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type Call struct {
	Tool    string
	Args    map[string]any
	Session string
}

type Memory struct {
	URL          string
	observations string
	mu           sync.Mutex
	calls        []Call
}

func NewMemory(t *testing.T, observations string) *Memory {
	t.Helper()
	m := &Memory{observations: observations}
	srv := httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(srv.Close)
	m.URL = srv.URL + "/mcp"
	return m
}

func (m *Memory) Calls() []Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Call(nil), m.calls...)
}

func (m *Memory) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     *int            `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "mcp-1")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-03-26\",\"capabilities\":{\"tools\":{}}}}\n\n"))
		return
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal(req.Params, &p)
	m.mu.Lock()
	m.calls = append(m.calls, Call{Tool: p.Name, Args: p.Arguments, Session: r.Header.Get("Mcp-Session-Id")})
	m.mu.Unlock()
	text := `{"path": "ok"}`
	if p.Name == "memory_read_session_observations" {
		text = m.observations
	}
	id := 0
	if req.ID != nil {
		id = *req.ID
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func WriteGraph(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "graphify-out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graphify-out", "graph.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func InstallQMD(t *testing.T, results string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qmd-bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	args := filepath.Join(dir, "args")
	script := "#!/usr/bin/env bash\nprintf '%s\\n' \"$@\" > " + args + "\ncat <<'JSON'\n" + results + "\nJSON\n"
	if err := os.WriteFile(filepath.Join(dir, "qmd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return args
}

func ClosedURL(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String() + "/mcp"
	_ = l.Close()
	return url
}
