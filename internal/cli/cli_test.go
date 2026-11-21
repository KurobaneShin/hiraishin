package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{name: "no args", args: nil, wantCode: 0, wantOut: "Usage:"},
		{name: "version", args: []string{"version"}, wantCode: 0, wantOut: "hiraishin "},
		{name: "help", args: []string{"--help"}, wantCode: 0, wantOut: "Commands:"},
		{name: "unknown", args: []string{"nope"}, wantCode: 2, wantErr: "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Run(context.Background(), tt.args, Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { return "" }})
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d", code, tt.wantCode)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want substring %q", out.String(), tt.wantOut)
			}
			if !strings.Contains(errOut.String(), tt.wantErr) {
				t.Errorf("stderr = %q, want substring %q", errOut.String(), tt.wantErr)
			}
		})
	}
}
