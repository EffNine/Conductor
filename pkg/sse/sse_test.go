package sse_test

import (
	"io"
	"strings"
	"testing"

	"github.com/novexa/gateway/pkg/sse"
)

func TestNewStreamReaderSkipsCommentOnlyKeepalives(t *testing.T) {
	body := io.NopCloser(strings.NewReader(
		": OPENROUTER PROCESSING\n\n" +
			": OPENROUTER PROCESSING\n\n" +
			"data: {\"ok\":true}\n\n" +
			"data: [DONE]\n\n",
	))

	events := make([]sse.Event, 0, 2)
	for ev := range sse.NewStreamReader(body) {
		events = append(events, ev)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (keepalive events skipped)", len(events))
	}
	if events[0].Data != `{"ok":true}` {
		t.Fatalf("first data = %q", events[0].Data)
	}
	if events[1].Data != "[DONE]" {
		t.Fatalf("second data = %q", events[1].Data)
	}
}
