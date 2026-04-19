package client

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSSEParser_Cases(t *testing.T) {
	type want struct {
		Type, Data, ID string
		Retry          time.Duration
		Keepalive      bool
	}
	cases := []struct {
		name  string
		in    string
		wants []want
	}{
		{
			name: "simple",
			in:   "event:foo\ndata:{\"k\":\"v\"}\nid:1\n\n",
			wants: []want{
				{Type: "foo", Data: `{"k":"v"}`, ID: "1"},
			},
		},
		{
			name: "leading-space-value",
			in:   "event: foo\ndata: {\"k\":\"v\"}\nid: 1\n\n",
			wants: []want{
				{Type: "foo", Data: `{"k":"v"}`, ID: "1"},
			},
		},
		{
			name: "multi-line-data",
			in:   "data:line1\ndata:line2\n\n",
			wants: []want{
				{Data: "line1\nline2"},
			},
		},
		{
			name: "keepalive",
			in:   ": keepalive\n\n",
			wants: []want{
				{Keepalive: true},
			},
		},
		{
			name: "retry",
			in:   "retry:5000\n\n",
			wants: []want{
				{Retry: 5000 * time.Millisecond},
			},
		},
		{
			name: "no-trailing-blank",
			in:   "event:foo\ndata:bar\nid:2\n",
			wants: []want{
				{Type: "foo", Data: "bar", ID: "2"},
			},
		},
		{
			name: "two-events",
			in:   "event:a\ndata:1\nid:1\n\nevent:b\ndata:2\nid:2\n\n",
			wants: []want{
				{Type: "a", Data: "1", ID: "1"},
				{Type: "b", Data: "2", ID: "2"},
			},
		},
		{
			name: "interleaved-keepalive",
			in:   "event:a\ndata:1\nid:1\n\n: keepalive\n\nevent:b\ndata:2\nid:2\n\n",
			wants: []want{
				{Type: "a", Data: "1", ID: "1"},
				{Keepalive: true},
				{Type: "b", Data: "2", ID: "2"},
			},
		},
		{
			name: "crlf-line-endings",
			in:   "event:foo\r\ndata:bar\r\nid:1\r\n\r\n",
			wants: []want{
				{Type: "foo", Data: "bar", ID: "1"},
			},
		},
		{
			name: "unknown-field-ignored",
			in:   "event:foo\nweird:value\ndata:bar\nid:1\n\n",
			wants: []want{
				{Type: "foo", Data: "bar", ID: "1"},
			},
		},
		{
			name: "retry-non-numeric-ignored",
			in:   "retry:abc\nevent:foo\ndata:bar\nid:1\n\n",
			wants: []want{
				{Type: "foo", Data: "bar", ID: "1"},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := NewSSEParser(strings.NewReader(tc.in))
			ctx := context.Background()
			for i, w := range tc.wants {
				ev, err := p.Next(ctx)
				if err != nil {
					t.Fatalf("event %d: Next returned err: %v", i, err)
				}
				if w.Keepalive {
					if !ev.IsKeepalive() {
						t.Fatalf("event %d: want keepalive, got %+v", i, ev)
					}
					continue
				}
				if ev.Type != w.Type {
					t.Errorf("event %d type: want %q got %q", i, w.Type, ev.Type)
				}
				if ev.Data != w.Data {
					t.Errorf("event %d data: want %q got %q", i, w.Data, ev.Data)
				}
				if ev.ID != w.ID {
					t.Errorf("event %d id: want %q got %q", i, w.ID, ev.ID)
				}
				if ev.Retry != w.Retry {
					t.Errorf("event %d retry: want %v got %v", i, w.Retry, ev.Retry)
				}
			}
			// Next call should be EOF.
			_, err := p.Next(ctx)
			if !errors.Is(err, io.EOF) {
				t.Errorf("final call: want io.EOF, got %v", err)
			}
		})
	}
}

// chunkedReader delivers bytes in fixed-size chunks to exercise partial
// reads across the SSE frame boundary.
type chunkedReader struct {
	data []byte
	n    int
	pos  int
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	end := c.pos + c.n
	if end > len(c.data) {
		end = len(c.data)
	}
	n := copy(p, c.data[c.pos:end])
	c.pos += n
	return n, nil
}

func TestSSEParser_PartialReads(t *testing.T) {
	in := "event:foo\ndata:bar\nid:1\n\nevent:baz\ndata:qux\nid:2\n\n"
	cr := &chunkedReader{data: []byte(in), n: 3}
	p := NewSSEParser(cr)
	ctx := context.Background()

	ev1, err := p.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ev1.Type != "foo" || ev1.Data != "bar" || ev1.ID != "1" {
		t.Errorf("ev1: got %+v", ev1)
	}
	ev2, err := p.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ev2.Type != "baz" || ev2.Data != "qux" || ev2.ID != "2" {
		t.Errorf("ev2: got %+v", ev2)
	}
}

func TestSSEParser_CtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := NewSSEParser(strings.NewReader("event:a\ndata:1\n\n"))
	_, err := p.Next(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
}
