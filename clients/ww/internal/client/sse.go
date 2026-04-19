// Package client provides the HTTP + SSE transport for the ww CLI.
package client

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// Event is a parsed SSE message.
type Event struct {
	Type        string
	Data        string
	ID          string
	Retry       time.Duration // zero when unset
	Comment     string        // captured comment text (may be empty for bare ":" blocks)
	CommentOnly bool          // true for blocks containing only SSE comments (keepalives)
}

// IsKeepalive reports whether this event is a bare SSE comment (keepalive).
func (e Event) IsKeepalive() bool {
	return e.CommentOnly
}

// SSEParser reads an SSE stream framed by blank-line separated blocks.
// Implements the subset of the SSE spec the harness actually emits plus
// the ": keepalive" comment line used to keep proxies awake.
type SSEParser struct {
	r *bufio.Reader
}

// NewSSEParser wraps r in an SSE frame reader.
func NewSSEParser(r io.Reader) *SSEParser {
	return &SSEParser{r: bufio.NewReaderSize(r, 64*1024)}
}

// Next returns the next event on the stream. At EOF it returns io.EOF.
// Malformed blocks are skipped silently (caller may wrap to log).
func (p *SSEParser) Next(ctx context.Context) (Event, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Event{}, err
		}
		ev, err := p.readBlock()
		if err != nil {
			// Pass through EOF with any tail-event still pending collected.
			return Event{}, err
		}
		// Drop entirely-empty blocks (repeated blank lines). Comment-
		// only blocks are surfaced via CommentOnly=true so callers can
		// recognise keepalives even when the comment text is empty.
		if ev.Type == "" && ev.Data == "" && ev.ID == "" && ev.Retry == 0 && ev.Comment == "" && !ev.CommentOnly {
			continue
		}
		return ev, nil
	}
}

// readBlock reads lines until it hits a blank line or EOF. A block with
// ONLY comment lines is marked CommentOnly=true (keepalive). The
// Comment field carries whatever text the comment actually contained;
// bare ": " keepalives leave Comment empty.
func (p *SSEParser) readBlock() (Event, error) {
	var (
		ev           Event
		dataLines    []string
		sawAnything  bool
		commentOnly  = true
		sawSomething bool
	)
	for {
		line, err := p.r.ReadString('\n')
		if len(line) == 0 && errors.Is(err, io.EOF) {
			if !sawAnything {
				return Event{}, io.EOF
			}
			// Flush partial block at EOF.
			if len(dataLines) > 0 {
				ev.Data = strings.Join(dataLines, "\n")
			}
			if commentOnly && sawSomething && ev.Data == "" && ev.Type == "" && ev.ID == "" {
				ev.CommentOnly = true
			}
			return ev, nil
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return ev, err
		}
		sawAnything = true
		// Strip trailing newline/CR.
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			// End of block.
			if len(dataLines) > 0 {
				ev.Data = strings.Join(dataLines, "\n")
			}
			if commentOnly && sawSomething && ev.Data == "" && ev.Type == "" && ev.ID == "" {
				ev.CommentOnly = true
			}
			return ev, nil
		}
		sawSomething = true
		// Comment line — skip.
		if strings.HasPrefix(line, ":") {
			// Record first non-empty comment text for diagnostics;
			// leave Comment empty for bare ":" / ": " blocks.
			if ev.Comment == "" {
				if text := strings.TrimSpace(line[1:]); text != "" {
					ev.Comment = text
				}
			}
			continue
		}
		commentOnly = false
		// field:value — "value" may be prefixed by a single space that
		// we trim per spec.
		var field, value string
		if idx := strings.IndexByte(line, ':'); idx >= 0 {
			field = line[:idx]
			value = line[idx+1:]
			value = strings.TrimPrefix(value, " ")
		} else {
			// Field-only line (rare); treat whole line as field with
			// empty value per SSE spec.
			field = line
		}
		switch field {
		case "event":
			ev.Type = value
		case "data":
			dataLines = append(dataLines, value)
		case "id":
			ev.ID = value
		case "retry":
			if ms, err := strconv.Atoi(value); err == nil && ms >= 0 {
				ev.Retry = time.Duration(ms) * time.Millisecond
			}
		default:
			// Unknown field — per spec, ignore silently.
		}
	}
}
