package agent

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

// LogsOptions — inputs to `ww agent logs`.
type LogsOptions struct {
	Agent     string
	Namespace string
	// Container — pod container to tail. Empty defaults to "harness"
	// (the orchestrator). Set to a backend name (echo/claude/codex/gemini)
	// to tail a specific backend sidecar.
	Container string
	// Follow — stream until context is cancelled. Default true.
	Follow bool
	// TailLines — emit at most this many lines of log history before
	// starting to follow. 0 = no limit.
	TailLines int64
	// Since — lookback window. Zero means "whatever the server returns".
	Since time.Duration
	// Pod — when non-empty, only tail this specific pod (by name).
	// Otherwise every pod matching the agent label is tailed. Useful
	// when an agent has been scaled or is mid-rollout.
	Pod string
	Out io.Writer
}

// Logs streams container logs for a WitwaveAgent's pods. Pattern mirrors
// internal/operator/logs.go: one goroutine per pod fan-in to a single
// writer under a mutex so line boundaries don't interleave.
func Logs(ctx context.Context, cfg *rest.Config, opts LogsOptions) error {
	if opts.Out == nil {
		return fmt.Errorf("LogsOptions.Out is required")
	}
	if opts.Agent == "" {
		return fmt.Errorf("LogsOptions.Agent is required")
	}
	container := opts.Container
	if container == "" {
		container = "harness"
	}

	k8s, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}

	pods, err := selectAgentPods(ctx, k8s, opts.Namespace, opts.Agent, opts.Pod)
	if err != nil {
		return err
	}
	if len(pods) == 0 {
		return fmt.Errorf(
			"no pods found for WitwaveAgent %q in namespace %s — is the operator installed and the agent Ready? Run `ww agent status %s`",
			opts.Agent, opts.Namespace, opts.Agent,
		)
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
	for _, pod := range pods {
		wg.Add(1)
		p := pod
		go func() {
			defer wg.Done()
			if err := streamAgentPodLogs(ctx, k8s, opts.Namespace, p, container, opts, writeLine); err != nil {
				errCh <- fmt.Errorf("pod %s: %w", p, err)
			}
		}()
	}
	wg.Wait()
	close(errCh)

	// Surface the first non-cancellation error. Ctrl-C is the normal
	// exit path and doesn't count as a failure.
	for e := range errCh {
		if ctx.Err() == nil {
			return e
		}
	}
	return nil
}

// selectAgentPods returns the pod names to tail for a given agent.
// Pattern mirrors internal/operator/logs.go:selectLogTargets.
func selectAgentPods(ctx context.Context, k8s kubernetes.Interface, ns, agentName, explicitPod string) ([]string, error) {
	if explicitPod != "" {
		// Sanity-check the pod exists; a missing pod surfaces a clear
		// error rather than a silently empty stream.
		if _, err := k8s.CoreV1().Pods(ns).Get(ctx, explicitPod, metav1.GetOptions{}); err != nil {
			return nil, fmt.Errorf("get pod %s/%s: %w", ns, explicitPod, err)
		}
		return []string{explicitPod}, nil
	}
	sel := labels.SelectorFromSet(labels.Set{
		"app.kubernetes.io/name": agentName,
	})
	list, err := k8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return nil, fmt.Errorf("list pods for agent %s in %s: %w", agentName, ns, err)
	}
	out := make([]string, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, list.Items[i].Name)
	}
	return out, nil
}

// streamAgentPodLogs opens a log stream for one (pod, container) and
// pumps each line through `emit`.
func streamAgentPodLogs(
	ctx context.Context,
	k8s kubernetes.Interface,
	ns, pod, container string,
	opts LogsOptions,
	emit func(pod, line string),
) error {
	podLogOpts := &corev1.PodLogOptions{
		Container: container,
		Follow:    opts.Follow,
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
		return fmt.Errorf("open log stream for container %q: %w", container, err)
	}
	defer stream.Close()

	// bufio buffer bump matches operator/logs.go — harness + backends
	// can emit >64 KiB lines on stack traces or bulky tool-audit entries.
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		emit(pod, scanner.Text())
		if ctx.Err() != nil {
			return nil
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("read log stream: %w", err)
	}
	return nil
}
