package agent

import (
	"context"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// EventsOptions — inputs to `ww agent events`.
type EventsOptions struct {
	Agent        string
	Namespace    string
	WarningsOnly bool
	Since        time.Duration
	Out          io.Writer
}

// Events returns a one-shot snapshot of Kubernetes events related to a
// single WitwaveAgent — both events on the CR itself and events on pods
// the operator owns for that agent. Unlike `ww operator events`, this
// does not support watch mode; when users need live streams, they're
// usually debugging a specific rollout and `ww agent logs -f` gives
// better signal.
func Events(ctx context.Context, cfg *rest.Config, opts EventsOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("EventsOptions.Out is required")
	}
	if opts.Agent == "" {
		return fmt.Errorf("EventsOptions.Agent is required")
	}
	if opts.Namespace == "" {
		return fmt.Errorf("EventsOptions.Namespace is required")
	}

	k8s, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}

	// CR-scoped events (involvedObject.kind=WitwaveAgent, name=<agent>).
	crEvents, err := listEventsWithFieldSelector(ctx, k8s, opts.Namespace,
		fields.AndSelectors(
			fields.OneTermEqualSelector("involvedObject.kind", Kind),
			fields.OneTermEqualSelector("involvedObject.name", opts.Agent),
		).String(),
	)
	if err != nil {
		return fmt.Errorf("list CR events: %w", err)
	}

	// Pod-scoped events. Operator-created pods carry
	// `app.kubernetes.io/name=<agent-name>`; fetch their names and pull
	// events one pod at a time (fieldSelector doesn't support OR).
	pods, err := selectAgentPods(ctx, k8s, opts.Namespace, opts.Agent, "")
	if err != nil {
		return err
	}
	var podEvents []corev1.Event
	for _, pod := range pods {
		evs, err := listEventsWithFieldSelector(ctx, k8s, opts.Namespace,
			fields.AndSelectors(
				fields.OneTermEqualSelector("involvedObject.kind", "Pod"),
				fields.OneTermEqualSelector("involvedObject.name", pod),
			).String(),
		)
		if err != nil {
			// Non-fatal — CR events still render; one pod's events
			// missing shouldn't block the rest.
			fmt.Fprintf(opts.Out, "warning: list events for pod %s: %v\n", pod, err)
			continue
		}
		podEvents = append(podEvents, evs...)
	}

	merged := append(crEvents, podEvents...)
	if opts.WarningsOnly {
		merged = filterWarnings(merged)
	}
	if opts.Since > 0 {
		cutoff := time.Now().Add(-opts.Since)
		merged = filterSince(merged, cutoff)
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return eventTime(merged[i]).Before(eventTime(merged[j]))
	})

	if len(merged) == 0 {
		fmt.Fprintf(opts.Out, "No events for WitwaveAgent %q in namespace %s.\n", opts.Agent, opts.Namespace)
		return nil
	}

	tw := tabwriter.NewWriter(opts.Out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "LAST SEEN\tTYPE\tOBJECT\tREASON\tMESSAGE")
	for _, e := range merged {
		obj := fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			FormatAge(eventTime(e)), e.Type, obj, e.Reason, e.Message,
		)
	}
	return tw.Flush()
}

// listEventsWithFieldSelector runs a scoped list against CoreV1().Events
// and returns the matching Event objects.
func listEventsWithFieldSelector(ctx context.Context, k8s kubernetes.Interface, ns, selector string) ([]corev1.Event, error) {
	list, err := k8s.CoreV1().Events(ns).List(ctx, metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// filterWarnings keeps only Type=Warning events.
func filterWarnings(evs []corev1.Event) []corev1.Event {
	out := make([]corev1.Event, 0, len(evs))
	for _, e := range evs {
		if e.Type == "Warning" {
			out = append(out, e)
		}
	}
	return out
}

// filterSince keeps only events at-or-after cutoff.
func filterSince(evs []corev1.Event, cutoff time.Time) []corev1.Event {
	out := make([]corev1.Event, 0, len(evs))
	for _, e := range evs {
		if eventTime(e).After(cutoff) {
			out = append(out, e)
		}
	}
	return out
}

// eventTime picks the most recent timestamp available on an Event.
// LastTimestamp is preferred (events series), falls back to
// EventTime (newer Events API), then CreationTimestamp.
func eventTime(e corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	return e.CreationTimestamp.Time
}
