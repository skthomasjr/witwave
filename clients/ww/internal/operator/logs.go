package operator

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// LogsOptions — inputs to `ww operator logs`.
type LogsOptions struct {
	Namespace string
	// Follow — stream until context is cancelled. Default true.
	Follow bool
	// TailLines — emit at most this many lines of log history before
	// starting to follow. 0 = no limit (emit full history). Default 100
	// keeps the first paint bounded without losing recent context.
	TailLines int64
	// Since — lookback window. Zero means "no SinceTime constraint"
	// (server-side default applies). Non-zero overrides TailLines'
	// implicit "last N lines" framing.
	Since time.Duration
	// Pod — when non-empty, only tail this specific pod (by name).
	// Otherwise every pod matching the operator label is tailed.
	Pod string
	// Out is where prefixed log lines go. Usually os.Stdout.
	Out io.Writer
}

// Logs streams the operator's pod logs. One goroutine per pod fans
// into a single Writer, protected by a mutex so line boundaries don't
// interleave. Follow mode blocks until the context is cancelled;
// no-follow returns when every pod's log stream EOFs.
func Logs(ctx context.Context, cfg *rest.Config, opts LogsOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("LogsOptions.Out is required")
	}
	k8s, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}

	pods, err := selectLogTargets(ctx, k8s, opts)
	if err != nil {
		return err
	}
	if len(pods) == 0 {
		return fmt.Errorf("no operator pods found in namespace %s matching "+
			"app.kubernetes.io/name=witwave-operator — is the operator installed?",
			opts.Namespace)
	}

	multi := len(pods) > 1
	var writeMu sync.Mutex
	writeLine := func(pod, line string) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if multi {
			fmt.Fprintf(opts.Out, "[%s] %s\n", pod, line)
		} else {
			fmt.Fprintln(opts.Out, line)
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(pods))

	for _, p := range pods {
		wg.Add(1)
		pod := p
		go func() {
			defer wg.Done()
			if err := streamPodLogs(ctx, k8s, opts.Namespace, pod, opts, writeLine); err != nil {
				// Stream-level errors are informational — one pod
				// dying shouldn't kill the whole tail.
				errCh <- fmt.Errorf("pod %s: %w", pod, err)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	// Surface the first non-cancellation error, if any. Context
	// cancellation (Ctrl-C) is the normal exit path and doesn't count.
	for e := range errCh {
		if ctx.Err() == nil {
			return e
		}
	}
	return nil
}

// selectLogTargets returns the list of pod names to tail. Honours the
// --pod override by verifying the named pod exists and is in the
// target namespace; otherwise lists every pod matching the operator
// label.
func selectLogTargets(ctx context.Context, k8s kubernetes.Interface, opts LogsOptions) ([]string, error) {
	if opts.Pod != "" {
		// Sanity-check the pod exists so the user gets a clear error
		// rather than a stream that silently ends.
		if _, err := k8s.CoreV1().Pods(opts.Namespace).Get(ctx, opts.Pod, metav1.GetOptions{}); err != nil {
			return nil, fmt.Errorf("get pod %s/%s: %w", opts.Namespace, opts.Pod, err)
		}
		return []string{opts.Pod}, nil
	}
	sel := labels.SelectorFromSet(labels.Set{
		"app.kubernetes.io/name": "witwave-operator",
	})
	list, err := k8s.CoreV1().Pods(opts.Namespace).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return nil, fmt.Errorf("list operator pods in %s: %w", opts.Namespace, err)
	}
	out := make([]string, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, list.Items[i].Name)
	}
	return out, nil
}

// streamPodLogs opens a log stream for one pod and pumps each line
// through `emit`. Respects context cancellation (Ctrl-C closes the
// stream cleanly).
func streamPodLogs(
	ctx context.Context,
	k8s kubernetes.Interface,
	ns, pod string,
	opts LogsOptions,
	emit func(pod, line string),
) error {
	podLogOpts := &corev1.PodLogOptions{
		Follow: opts.Follow,
	}
	if opts.TailLines > 0 {
		tail := opts.TailLines
		podLogOpts.TailLines = &tail
	}
	if opts.Since > 0 {
		ts := metav1.NewTime(time.Now().Add(-opts.Since))
		podLogOpts.SinceTime = &ts
	}

	req := k8s.CoreV1().Pods(ns).GetLogs(pod, podLogOpts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("open log stream: %w", err)
	}
	defer stream.Close()

	// bufio.Scanner defaults to 64 KiB lines; controller-runtime's
	// structured logger emits more than that occasionally (stack
	// traces, big reconcile payloads). Bump the buffer to 1 MiB so
	// those lines don't get truncated to "bufio.Scanner: token too
	// long".
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		emit(pod, scanner.Text())
		// Respect cancellation on every iteration — scanner doesn't
		// observe ctx on its own.
		if ctx.Err() != nil {
			return nil
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("read log stream: %w", err)
	}
	return nil
}
