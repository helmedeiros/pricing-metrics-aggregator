package jsonlog

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestLoggerInfoEmitsPlatformShape(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.Info("test.event", map[string]any{"k": "v"})
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; raw=%q", err, buf.String())
	}
	if got["msg"] != "test.event" || got["level"] != "info" {
		t.Errorf("msg=%v level=%v", got["msg"], got["level"])
	}
	attrs, ok := got["attrs"].(map[string]any)
	if !ok || attrs["k"] != "v" {
		t.Errorf("attrs = %v", got["attrs"])
	}
}

func TestLoggerLevels(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.Warn("w", nil)
	l.Error("e", nil)
	if !bytes.Contains(buf.Bytes(), []byte(`"level":"warn"`)) {
		t.Error("warn level missing")
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"level":"error"`)) {
		t.Error("error level missing")
	}
}
