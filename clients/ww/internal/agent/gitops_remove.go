package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// GitRemoveOptions are inputs to `ww agent git remove`.
type GitRemoveOptions struct {
	Agent     string
	Namespace string

	// SyncName selects which gitSyncs[] entry to remove. Default
	// DefaultGitSyncName ("witwave") — the conventional entry that
	// `ww agent git add` creates.
	SyncName string

	// DeleteSecret, when true, also deletes the K8s Secret the operator
	// minted for this sync (if it's ww-managed). Off by default —
	// removing git access and deleting the Secret are orthogonal, and
	// the safe default is to preserve credentials for potential re-
	// attach via `ww agent git add --auth-secret <same-name>`.
	DeleteSecret bool

	AssumeYes bool
	DryRun    bool
	Out       io.Writer
	In        io.Reader
}

// GitRemove detaches the named gitSync entry from a WitwaveAgent. Drops
// the gitSyncs[] entry itself, removes every harness + backend
// gitMapping tied to the entry's name, and optionally deletes the
// ww-minted credential Secret.
//
// Preserves mappings tied to other gitSyncs (if any) untouched.
// Preserves user-created Secrets (unlabelled) even under --delete-secret.
func GitRemove(
	ctx context.Context,
	cfg *rest.Config,
	opts GitRemoveOptions,
) error {
	if opts.Out == nil {
		return fmt.Errorf("GitRemoveOptions.Out is required")
	}
	if err := ValidateName(opts.Agent); err != nil {
		return err
	}

	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}
	cr, err := fetchAgentCR(ctx, dyn, opts.Namespace, opts.Agent)
	if err != nil {
		return err
	}

	syncs, err := readGitSyncs(cr)
	if err != nil {
		return err
	}

	// Default sync-name resolution. When the caller didn't pass
	// --sync-name, auto-select the agent's only gitSync (common case).
	// Zero syncs = nothing to remove, crisp error. Multiple syncs =
	// refuse with the full list so the user can pick one by name.
	syncName := opts.SyncName
	if syncName == "" {
		switch len(syncs) {
		case 0:
			return fmt.Errorf(
				"WitwaveAgent %s/%s has no gitSyncs configured — nothing to remove",
				opts.Namespace, opts.Agent,
			)
		case 1:
			syncName, _ = syncs[0]["name"].(string)
		default:
			names := make([]string, 0, len(syncs))
			for _, s := range syncs {
				if n, _ := s["name"].(string); n != "" {
					names = append(names, n)
				}
			}
			return fmt.Errorf(
				"WitwaveAgent %s/%s has %d gitSyncs [%s] — pick one with --sync-name <name>",
				opts.Namespace, opts.Agent, len(syncs), strings.Join(names, ", "),
			)
		}
	}

	idx, entry := syncEntryByName(syncs, syncName)
	if idx < 0 {
		return fmt.Errorf(
			"gitSync %q not found on WitwaveAgent %s/%s (run `ww agent git list %s` to see configured syncs)",
			syncName, opts.Namespace, opts.Agent, opts.Agent,
		)
	}

	// Figure out the Secret name from the entry before we drop it,
	// so --delete-secret has something to reference.
	var secretToDelete string
	if creds, ok := entry["credentials"].(map[string]interface{}); ok {
		if sec, ok := creds["existingSecret"].(string); ok {
			secretToDelete = sec
		}
	}

	// Banner.
	fmt.Fprintf(opts.Out, "\nAction:    detach gitSync %q from WitwaveAgent %q in %s\n",
		syncName, opts.Agent, opts.Namespace)
	if opts.DeleteSecret && secretToDelete != "" {
		fmt.Fprintf(opts.Out, "  also: delete ww-managed Secret %q (if ww-labelled)\n", secretToDelete)
	}

	if opts.DryRun {
		fmt.Fprintln(opts.Out, "Dry-run mode — no API calls made.")
		return nil
	}

	// Drop the sync + tied mappings from the CR.
	syncs = append(syncs[:idx], syncs[idx+1:]...)
	if err := writeGitSyncs(cr, syncs); err != nil {
		return err
	}

	harnessMaps, err := readHarnessGitMappings(cr)
	if err != nil {
		return err
	}
	if err := writeHarnessGitMappings(cr, filterMappingsByGitSync(harnessMaps, syncName)); err != nil {
		return err
	}

	backends, err := readBackends(cr)
	if err != nil {
		return err
	}
	for i, b := range backends {
		existing, _ := b["gitMappings"].([]interface{})
		kept := make([]interface{}, 0, len(existing))
		for _, raw := range existing {
			m, ok := raw.(map[string]interface{})
			if !ok {
				kept = append(kept, raw)
				continue
			}
			if n, _ := m["gitSync"].(string); n == syncName {
				continue
			}
			kept = append(kept, m)
		}
		if len(kept) == 0 {
			delete(b, "gitMappings")
		} else {
			b["gitMappings"] = kept
		}
		backends[i] = b
	}
	if err := writeBackends(cr, backends); err != nil {
		return err
	}

	if _, err := updateAgentCR(ctx, dyn, cr); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "Detached gitSync %q from WitwaveAgent %s/%s.\n",
		syncName, opts.Namespace, opts.Agent)

	if opts.DeleteSecret && secretToDelete != "" {
		k8sClient, err := newKubernetesClient(cfg)
		if err != nil {
			return err
		}
		if err := deleteIfWWManaged(ctx, k8sClient, opts.Namespace, secretToDelete, opts.Out); err != nil {
			return err
		}
	}
	return nil
}

// deleteIfWWManaged deletes the Secret only when its managed-by label
// matches ww's value. User-created Secrets under the same name are
// preserved — the label gate is the same "never clobber what we
// didn't create" check used by upsertGitCredentialSecret.
func deleteIfWWManaged(ctx context.Context, k8s kubernetes.Interface, namespace, name string, out io.Writer) error {
	existing, err := k8s.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			fmt.Fprintf(out, "Secret %s/%s already absent — nothing to delete.\n", namespace, name)
			return nil
		}
		return fmt.Errorf("get Secret %s/%s: %w", namespace, name, err)
	}
	if existing.Labels[LabelManagedBy] != LabelManagedByWW {
		fmt.Fprintf(out,
			"Secret %s/%s exists but is not ww-managed; preserved. "+
				"Delete manually if you want it gone: kubectl -n %s delete secret %s\n",
			namespace, name, namespace, name,
		)
		return nil
	}
	if err := k8s.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("delete Secret %s/%s: %w", namespace, name, err)
	}
	fmt.Fprintf(out, "Deleted ww-managed Secret %s/%s.\n", namespace, name)
	return nil
}
