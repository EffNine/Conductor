package sse

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Event represents a Server-Sent Event
type Event struct {
	Event string
	Data  string
	ID    string
	Retry int
}

// Parse parses an SSE event from a byte slice
func Parse(data []byte) (*Event, error) {
	event := &Event{}
	scanner := bufio.NewScanner(bytes.NewReader(data))

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, ":") {
			// Comment, skip
			continue
		}

		colonIndex := strings.Index(line, ":")
		if colonIndex == -1 {
			// Field name only
			switch line {
			case "data":
				event.Data += "\n"
			}
			continue
		}

		field := line[:colonIndex]
		value := ""
		if colonIndex+1 < len(line) {
			value = line[colonIndex+1:]
			// Remove leading space if present
			value = strings.TrimPrefix(value, " ")
		}

		switch field {
		case "event":
			event.Event = value
		case "data":
			if event.Data != "" {
				event.Data += "\n"
			}
			event.Data += value
		case "id":
			event.ID = value
		case "retry":
			_, _ = fmt.Sscanf(value, "%d", &event.Retry)
		}
	}

	return event, scanner.Err()
}

// Marshal formats an Event as SSE bytes
func Marshal(event *Event) []byte {
	var buf bytes.Buffer

	if event.Event != "" {
		buf.WriteString(fmt.Sprintf("event: %s\n", event.Event))
	}

	if event.Data != "" {
		lines := strings.Split(event.Data, "\n")
		for _, line := range lines {
			buf.WriteString(fmt.Sprintf("data: %s\n", line))
		}
	}

	if event.ID != "" {
		buf.WriteString(fmt.Sprintf("id: %s\n", event.ID))
	}

	if event.Retry > 0 {
		buf.WriteString(fmt.Sprintf("retry: %d\n", event.Retry))
	}

	buf.WriteString("\n")
	return buf.Bytes()
}

// WriteEvent writes an SSE event to a writer
func WriteEvent(w io.Writer, event *Event) error {
	_, err := w.Write(Marshal(event))
	return err
}

// WriteData writes a data-only SSE event
func WriteData(w io.Writer, data string) error {
	return WriteEvent(w, &Event{Data: data})
}

// WriteDone writes the [DONE] sentinel event
func WriteDone(w io.Writer) error {
	_, err := fmt.Fprintf(w, "data: [DONE]\n\n")
	return err
}

// NewStreamReader creates a channel that reads SSE events from a reader
func NewStreamReader(r io.ReadCloser) <-chan Event {
	ch := make(chan Event)

	go func() {
		defer close(ch)
		defer r.Close()

		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var currentEvent Event
		for scanner.Scan() {
			line := scanner.Text()

			if line == "" {
				// Empty line = end of event
				ch <- currentEvent
				currentEvent = Event{}
				continue
			}

			if strings.HasPrefix(line, ":") {
				continue
			}

			colonIndex := strings.Index(line, ":")
			if colonIndex == -1 {
				continue
			}

			field := line[:colonIndex]
			value := ""
			if colonIndex+1 < len(line) {
				value = line[colonIndex+1:]
				value = strings.TrimPrefix(value, " ")
			}

			switch field {
			case "event":
				currentEvent.Event = value
			case "data":
				if currentEvent.Data != "" {
					currentEvent.Data += "\n"
				}
				currentEvent.Data += value
			case "id":
				currentEvent.ID = value
			case "retry":
				_, _ = fmt.Sscanf(value, "%d", &currentEvent.Retry)
			}
		}
	}()

	return ch
}
