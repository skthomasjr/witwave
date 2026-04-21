package operator

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// EventsOptions — inputs to `ww operator events`.
type EventsOptions struct {
	// Namespace — when non-empty, scope CR-event listing to this namespace
	// only. Empty (default) lists across all namespaces — CRs typically
	// live wherever the user deploys them, not in witwave-system.
	Namespace string
	// OperatorNamespace — where operator pods live. Events against those
	// pods are always included in the output. Defaults to witwave-system.
	OperatorNamespace string
	// Watch — stream new events until context is cancelled. Default false
	// (one-shot list + print). Like kubectl events --watch.
	Watch bool
	// WarningsOnly — filter to type=Warning events. Most useful for
	// "what's going wrong?" diagnostics.
	WarningsOnly bool
	// Since — lookback window. Zero means "whatever the apiserver returns"
	// (typically the last hour's worth of retained events).
	Since time.Duration
	// Out is where table rows go. Usually os.Stdout.
	Out io.Writer
}

// kindsOfInterest lists the involvedObject.kind values the operator
// emits events against. Expand this list as the operator grows (e.g.
// future Witwave* kinds).
var kindsOfInterest = []string{"WitwaveAgent", "WitwavePrompt"}

// Events fetches + renders events related to the witwave operator.
// Three sources are merged:
//
//  1. Events where involvedObject.kind=WitwaveAgent (any namespace,
//     or limited by opts.Namespace).
//  2. Events where involvedObject.kind=WitwavePrompt (same scope).
//  3. Events in the operator's own namespace (witwave-system by
//     default) — catches Pod scheduling failures, crash loops, image
//     pull errors on the operator itself.
//
// When opts.Watch is true, the function opens watch streams for each
// source and streams new events until the context is cancelled.
// Otherwise it returns after the initial snapshot is printed.
func Events(ctx context.Context, cfg *rest.Config, opts EventsOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("EventsOptions.Out is required")
	}
	if opts.OperatorNamespace == "" {
		opts.OperatorNamespace = DefaultNamespace
	}

	k8s, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}

	// --- Initial snapshot ---
	snap, err := listInitialEvents(ctx, k8s, opts)
	if err != nil {
		return err
	}
	renderEvents(opts.Out, snap.events, opts)

	if !opts.Watch {
		return nil
	}

	// --- Watch mode: one stream per source, fanned into a single writer. ---
	var writeMu sync.Mutex
	writeEvent := func(ev *corev1.Event) {
		if opts.WarningsOnly && ev.Type != corev1.EventTypeWarning {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		tw := tabwriter.NewWriter(opts.Out, 2, 2, 2, ' ', 0)
		writeEventRow(tw, ev)
		_ = tw.Flush()
	}

	// Resource version to resume from — each source uses the RV returned
	// by *its own* List call (list.ResourceVersion). Mixing RVs across
	// sources can trigger TooLargeResourceVersion on a source whose
	// storage lags, or drop events on a source whose storage leads
	// (#1546). An empty RV from List is passed through unchanged; the
	// apiserver interprets that as "resume from now."
	var wg sync.WaitGroup
	errCh := make(chan error, len(kindsOfInterest)+1)

	for _, kind := range kindsOfInterest {
		wg.Add(1)
		k := kind
		kindRV := snap.kindRVs[k]
		go func() {
			defer wg.Done()
			if err := watchKindEvents(ctx, k8s, opts.Namespace, k, kindRV, writeEvent); err != nil && ctx.Err() == nil {
				errCh <- fmt.Errorf("watch %s events: %w", k, err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := watchNamespaceEvents(ctx, k8s, opts.OperatorNamespace, snap.nsEventRV, writeEvent); err != nil && ctx.Err() == nil {
			errCh <- fmt.Errorf("watch %s namespace events: %w", opts.OperatorNamespace, err)
		}
	}()

	wg.Wait()
	close(errCh)
	for e := range errCh {
		return e
	}
	return nil
}

// initialSnapshot is the result of listInitialEvents: the deduped + filtered
// event set plus the per-source list ResourceVersions. Each watch stream
// must resume from the RV returned by *its own* List call — mixing RVs
// across sources causes TooLargeResourceVersion errors or silent gaps
// when one source's storage is ahead of another's (#1546).
type initialSnapshot struct {
	events    []corev1.Event
	kindRVs   map[string]string // kind -> list.ResourceVersion for that kind
	nsEventRV string            // list.ResourceVersion for operator-namespace events
}

// listInitialEvents runs the three LIST calls, deduplicates, filters
// by opts.WarningsOnly + opts.Since, and sorts oldest-first.
func listInitialEvents(ctx context.Context, k8s kubernetes.Interface, opts EventsOptions) (*initialSnapshot, error) {
	seen := map[string]struct{}{}
	var all []corev1.Event

	add := func(items []corev1.Event) {
		for i := range items {
			ev := items[i]
			if _, ok := seen[string(ev.UID)]; ok {
				continue
			}
			seen[string(ev.UID)] = struct{}{}
			if opts.WarningsOnly && ev.Type != corev1.EventTypeWarning {
				continue
			}
			if opts.Since > 0 {
				ts := effectiveTimestamp(&ev)
				if ts.IsZero() || time.Since(ts) > opts.Since {
					continue
				}
			}
			all = append(all, ev)
		}
	}

	snap := &initialSnapshot{kindRVs: map[string]string{}}

	// Per-kind list across the requested namespace (empty = all namespaces).
	for _, kind := range kindsOfInterest {
		list, err := listEventsForKind(ctx, k8s, opts.Namespace, kind)
		if err != nil {
			return nil, err
		}
		snap.kindRVs[kind] = list.ResourceVersion
		add(list.Items)
	}

	// Operator-namespace pod/deployment events.
	list, err := k8s.CoreV1().Events(opts.OperatorNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list events in %s: %w", opts.OperatorNamespace, err)
	}
	snap.nsEventRV = list.ResourceVersion
	add(list.Items)

	sort.Slice(all, func(i, j int) bool {
		return effectiveTimestamp(&all[i]).Before(effectiveTimestamp(&all[j]))
	})
	snap.events = all
	return snap, nil
}

func listEventsForKind(ctx context.Context, k8s kubernetes.Interface, ns, kind string) (*corev1.EventList, error) {
	sel := fields.OneTermEqualSelector("involvedObject.kind", kind).String()
	list, err := k8s.CoreV1().Events(ns).List(ctx, metav1.ListOptions{FieldSelector: sel})
	if err != nil {
		return nil, fmt.Errorf("list %s events: %w", kind, err)
	}
	return list, nil
}

func watchKindEvents(ctx context.Context, k8s kubernetes.Interface, ns, kind, rv string, emit func(*corev1.Event)) error {
	sel := fields.OneTermEqualSelector("involvedObject.kind", kind).String()
	w, err := k8s.CoreV1().Events(ns).Watch(ctx, metav1.ListOptions{
		FieldSelector:   sel,
		ResourceVersion: rv,
	})
	if err != nil {
		return err
	}
	return pumpWatch(ctx, w, emit)
}

func watchNamespaceEvents(ctx context.Context, k8s kubernetes.Interface, ns, rv string, emit func(*corev1.Event)) error {
	w, err := k8s.CoreV1().Events(ns).Watch(ctx, metav1.ListOptions{
		ResourceVersion: rv,
	})
	if err != nil {
		return err
	}
	return pumpWatch(ctx, w, emit)
}

// pumpWatch drains a watch.Interface until it closes or the context
// ends, emitting Added/Modified events and ignoring Deleted/Bookmark
// frames (events aren't deleted in a way that's useful to display).
func pumpWatch(ctx context.Context, w watch.Interface, emit func(*corev1.Event)) error {
	defer w.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.ResultChan():
			if !ok {
				return nil
			}
			switch ev.Type {
			case watch.Added, watch.Modified:
				e, ok := ev.Object.(*corev1.Event)
				if ok && e != nil {
					emit(e)
				}
			case watch.Error:
				return fmt.Errorf("watch error: %v", ev.Object)
			}
		}
	}
}

// --- rendering ---

func renderEvents(out io.Writer, events []corev1.Event, opts EventsOptions) {
	if len(events) == 0 {
		if opts.WarningsOnly {
			fmt.Fprintln(out, "No warning events in scope.")
		} else {
			fmt.Fprintln(out, "No events in scope.")
		}
		return
	}
	tw := tabwriter.NewWriter(out, 2, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "LAST SEEN\tTYPE\tOBJECT\tREASON\tMESSAGE")
	for i := range events {
		writeEventRow(tw, &events[i])
	}
	_ = tw.Flush()
}

func writeEventRow(w io.Writer, ev *corev1.Event) {
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
		ageOrExact(effectiveTimestamp(ev)),
		ev.Type,
		describeObject(ev),
		ev.Reason,
		truncateMessage(ev.Message, 120),
	)
}

// effectiveTimestamp returns the most recent timestamp we have for an
// event. v1.Event coalesces, so LastTimestamp is usually the one
// users want; falls back to FirstTimestamp, then EventTime (for
// events.k8s.io/v1-shaped data that leaks through), then
// CreationTimestamp.
func effectiveTimestamp(ev *corev1.Event) time.Time {
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp.Time
	}
	if !ev.FirstTimestamp.IsZero() {
		return ev.FirstTimestamp.Time
	}
	if !ev.EventTime.IsZero() {
		return ev.EventTime.Time
	}
	return ev.CreationTimestamp.Time
}

// ageOrExact renders a timestamp as "2m ago" for recent events,
// falling back to RFC3339 for anything older than 24h or in the
// future (clock skew).
func ageOrExact(t time.Time) string {
	if t.IsZero() {
		return "<unknown>"
	}
	d := time.Since(t)
	if d < 0 || d > 24*time.Hour {
		return t.Format("2006-01-02T15:04:05Z07:00")
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// describeObject formats the involvedObject ref as "Kind/name" or
// "Kind/name (ns)" when the event's namespace differs from the
// involvedObject's — rare but possible for cross-namespace refs.
func describeObject(ev *corev1.Event) string {
	k := ev.InvolvedObject.Kind
	n := ev.InvolvedObject.Name
	if k == "" {
		k = "?"
	}
	if n == "" {
		n = "?"
	}
	base := k + "/" + n
	if ev.InvolvedObject.Namespace != "" && ev.InvolvedObject.Namespace != ev.Namespace {
		base += " (" + ev.InvolvedObject.Namespace + ")"
	}
	return base
}

func truncateMessage(msg string, max int) string {
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) <= max {
		return msg
	}
	return msg[:max-1] + "…"
}
