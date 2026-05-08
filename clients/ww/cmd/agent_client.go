package cmd

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/witwave-ai/witwave/clients/ww/internal/client"
	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
	"github.com/witwave-ai/witwave/clients/ww/internal/portforward"
)

// openAgentClient port-forwards to a WitwaveAgent's harness pod and
// returns an HTTP client.Client targeted at the loopback URL. The
// matching cleanup func MUST be deferred by the caller — closing it
// tears down the port-forward.
//
// Used by `ww send` and `ww tail` so the user can talk to an agent by
// name without manually running `kubectl port-forward + --base-url +
// --token`. Mirrors the auto-port-forward shape `ww conversation`
// already established; the helper is shared so future harness-HTTP
// commands drop in cleanly.
//
// Token resolution:
//
//   - If `tokenOverride` is non-empty, it wins (mirrors --token flag).
//   - Otherwise, read CONVERSATIONS_AUTH_TOKEN from the agent's
//     `<agent>-claude` Secret. Missing key / unreadable secret falls
//     back to empty string — which the harness handles per its
//     CONVERSATIONS_AUTH_DISABLED=true / 503-fail-closed posture
//     (the resulting error message tells the user what's wrong).
//
// The returned client has the same Verbose / Timeout posture as the
// root context's existing client so logging and timeouts match what
// the user would have seen with manual port-forward + flags.
func openAgentClient(
	ctx context.Context,
	kcFlags K8sFlags,
	namespace, agent string,
	tokenOverride string,
) (*client.Client, func(), error) {
	resolver, err := k8s.NewResolver(k8s.Options{
		KubeconfigPath: kcFlags.Kubeconfig,
		Context:        kcFlags.Context,
		Namespace:      namespace,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("kubeconfig: %w", err)
	}
	cfg, err := resolver.REST()
	if err != nil {
		return nil, nil, fmt.Errorf("kubeconfig REST config: %w", err)
	}
	resolvedNS := namespace
	if resolvedNS == "" {
		resolvedNS = resolver.Target().Namespace
	}
	if resolvedNS == "" {
		resolvedNS = "witwave"
	}

	fwd, err := portforward.OpenPort(ctx, cfg, resolvedNS, agent, portforward.HarnessHTTPPort)
	if err != nil {
		return nil, nil, fmt.Errorf("port-forward to %s/%s: %w", resolvedNS, agent, err)
	}

	token := tokenOverride
	if token == "" {
		token = readAgentConversationsToken(ctx, cfg, resolvedNS, agent)
	}

	c := client.New(client.Config{
		BaseURL: fwd.BaseURL,
		Token:   token,
		Timeout: 60 * time.Second, // matches the default snapshot timeout
	})

	cleanup := func() { fwd.Close() }
	return c, cleanup, nil
}

// readAgentConversationsToken reads CONVERSATIONS_AUTH_TOKEN from the
// agent's credentials Secret (`<agent>-claude` by convention). Returns
// empty string when the secret or key is absent — caller's HTTP request
// will get a 503 "auth not configured" if the harness has token-auth
// configured, or work fine if CONVERSATIONS_AUTH_DISABLED=true.
func readAgentConversationsToken(ctx context.Context, cfg *rest.Config, namespace, agent string) string {
	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return ""
	}
	secretName := agent + "-claude"
	s, err := k8sClient.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
			return ""
		}
		return ""
	}
	if v, ok := s.Data["CONVERSATIONS_AUTH_TOKEN"]; ok {
		return string(v)
	}
	return ""
}
