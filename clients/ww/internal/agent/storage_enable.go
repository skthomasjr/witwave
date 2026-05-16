package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/witwave-ai/witwave/clients/ww/internal/k8s"
)

const runtimeLogsPath = "/home/agent/logs"

// StorageEnableOptions controls `ww agent storage enable <name>`.
type StorageEnableOptions struct {
	Name      string
	Namespace string

	// RuntimeSize is used when runtimeStorage is absent or has no size.
	// Defaults to 1Gi when omitted.
	RuntimeSize string

	// RuntimeStorageClassName optionally stamps spec.runtimeStorage.storageClassName.
	// Existing runtimeStorage keeps its class unless this value is supplied.
	RuntimeStorageClassName string

	// BackendState adds /home/agent/state to backends that already have
	// persistent storage enabled. Backends without storage are intentionally
	// skipped rather than silently creating backend PVCs.
	BackendState bool

	Wait    bool
	Timeout time.Duration

	AssumeYes bool
	DryRun    bool
	Out       io.Writer
	In        io.Reader
}

// StorageEnable enables the default durable runtime storage layout on an
// existing WitwaveAgent CR. It is intentionally narrower than agent create's
// --with-persistence path: existing backend PVCs get a state subPath, but
// backends without storage are left alone so the command cannot unexpectedly
// allocate large backend volumes on already-running agents.
func StorageEnable(
	ctx context.Context,
	target *k8s.Target,
	cfg *rest.Config,
	opts StorageEnableOptions,
) error {
	if opts.Out == nil {
		return fmt.Errorf("StorageEnableOptions.Out is required")
	}
	if opts.Name == "" {
		return fmt.Errorf("StorageEnableOptions.Name is required")
	}
	if opts.Namespace == "" {
		return fmt.Errorf("StorageEnableOptions.Namespace is required")
	}
	if opts.RuntimeSize == "" {
		opts.RuntimeSize = "1Gi"
	}

	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return err
	}
	cr, err := fetchAgentCR(ctx, dyn, opts.Namespace, opts.Name)
	if err != nil {
		return err
	}
	working := cr.DeepCopy()

	changes, err := applyStorageEnableInPlace(working, storageEnableConfig{
		RuntimeSize:             opts.RuntimeSize,
		RuntimeStorageClassName: opts.RuntimeStorageClassName,
		BackendState:            opts.BackendState,
	})
	if err != nil {
		return err
	}
	if !changes.Changed() {
		fmt.Fprintf(opts.Out, "WitwaveAgent %s/%s already has runtime storage configured", opts.Namespace, opts.Name)
		if opts.BackendState {
			fmt.Fprint(opts.Out, " and every persistent backend already has /home/agent/state")
		}
		fmt.Fprintln(opts.Out, ".")
		return nil
	}

	plan := []k8s.PlanLine{
		{Key: "Action", Value: fmt.Sprintf("enable runtime storage on WitwaveAgent %q", opts.Name)},
		{Key: "Runtime storage", Value: runtimeStoragePlanValue(working, opts.Name)},
		{Key: "Harness mounts", Value: runtimeLogsPath + ", " + runtimeStatePath},
		{Key: "Task store", Value: RuntimeTaskStorePath},
	}
	if opts.RuntimeStorageClassName != "" {
		plan = append(plan, k8s.PlanLine{Key: "Storage class", Value: opts.RuntimeStorageClassName})
	}
	if len(changes.BackendStateAdded) > 0 {
		plan = append(plan, k8s.PlanLine{
			Key:   "Backend state",
			Value: "add /home/agent/state to " + strings.Join(changes.BackendStateAdded, ", "),
		})
	}
	if len(changes.BackendStateSkipped) > 0 {
		plan = append(plan, k8s.PlanLine{
			Key:   "Skipped backends",
			Value: strings.Join(changes.BackendStateSkipped, ", "),
		})
	}

	proceed, err := k8s.Confirm(opts.Out, opts.In, target, plan, k8s.PromptOptions{
		AssumeYes: opts.AssumeYes,
		DryRun:    opts.DryRun,
	})
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	if _, err := updateAgentCR(ctx, dyn, working); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "Updated WitwaveAgent %s/%s.\n", opts.Namespace, opts.Name)

	if !opts.Wait {
		fmt.Fprintln(opts.Out, "Skipping rollout wait (--no-wait). Check with `ww agent status "+opts.Name+"`.")
		return nil
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	fmt.Fprintf(opts.Out, "Waiting up to %s for the operator to roll the deployment to Ready...\n", timeout)
	k8sClient, err := newKubernetesClient(cfg)
	if err != nil {
		return err
	}
	if err := waitForReady(ctx, dyn, k8sClient, opts.Namespace, opts.Name, timeout, opts.Out); err != nil {
		return err
	}
	if err := waitForDeploymentRollout(ctx, k8sClient, opts.Namespace, opts.Name, timeout, opts.Out); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "\nRuntime storage enabled for agent %s.\n", opts.Name)
	return nil
}

type storageEnableConfig struct {
	RuntimeSize             string
	RuntimeStorageClassName string
	BackendState            bool
}

type storageEnableChanges struct {
	RuntimeStorageChanged bool
	BackendStateAdded     []string
	BackendStateSkipped   []string
}

func (c storageEnableChanges) Changed() bool {
	return c.RuntimeStorageChanged || len(c.BackendStateAdded) > 0
}

func applyStorageEnableInPlace(cr *unstructured.Unstructured, cfg storageEnableConfig) (storageEnableChanges, error) {
	var changes storageEnableChanges
	if cfg.RuntimeSize == "" {
		cfg.RuntimeSize = "1Gi"
	}
	runtimeChanged, err := ensureRuntimeStorageInPlace(cr, cfg.RuntimeSize, cfg.RuntimeStorageClassName)
	if err != nil {
		return changes, err
	}
	changes.RuntimeStorageChanged = runtimeChanged

	if cfg.BackendState {
		added, skipped, err := ensureBackendStateMountsInPlace(cr)
		if err != nil {
			return changes, err
		}
		changes.BackendStateAdded = added
		changes.BackendStateSkipped = skipped
	}
	return changes, nil
}

func ensureRuntimeStorageInPlace(cr *unstructured.Unstructured, size, storageClass string) (bool, error) {
	storage, found, err := unstructured.NestedMap(cr.Object, "spec", "runtimeStorage")
	if err != nil {
		return false, fmt.Errorf("read spec.runtimeStorage: %w", err)
	}
	changed := false
	if !found {
		storage = map[string]interface{}{}
		changed = true
	}

	if enabled, found, err := unstructured.NestedBool(storage, "enabled"); err != nil {
		return false, fmt.Errorf("read spec.runtimeStorage.enabled: %w", err)
	} else if !found || !enabled {
		storage["enabled"] = true
		changed = true
	}

	existingClaim, _, err := unstructured.NestedString(storage, "existingClaim")
	if err != nil {
		return false, fmt.Errorf("read spec.runtimeStorage.existingClaim: %w", err)
	}
	if strings.TrimSpace(existingClaim) == "" {
		currentSize, _, err := unstructured.NestedString(storage, "size")
		if err != nil {
			return false, fmt.Errorf("read spec.runtimeStorage.size: %w", err)
		}
		if strings.TrimSpace(currentSize) == "" {
			storage["size"] = size
			changed = true
		}
	}
	if storageClass != "" {
		currentClass, _, err := unstructured.NestedString(storage, "storageClassName")
		if err != nil {
			return false, fmt.Errorf("read spec.runtimeStorage.storageClassName: %w", err)
		}
		if currentClass != storageClass {
			storage["storageClassName"] = storageClass
			changed = true
		}
	}

	mounts, mountChanged, err := ensureMounts(storage["mounts"], []runtimeMount{
		{SubPath: "logs", MountPath: runtimeLogsPath},
		{SubPath: "state", MountPath: runtimeStatePath},
	})
	if err != nil {
		return false, fmt.Errorf("read spec.runtimeStorage.mounts: %w", err)
	}
	if mountChanged {
		storage["mounts"] = mounts
		changed = true
	}

	if err := unstructured.SetNestedMap(cr.Object, storage, "spec", "runtimeStorage"); err != nil {
		return false, fmt.Errorf("set spec.runtimeStorage: %w", err)
	}
	return changed, nil
}

func ensureBackendStateMountsInPlace(cr *unstructured.Unstructured) ([]string, []string, error) {
	backends, err := readBackends(cr)
	if err != nil {
		return nil, nil, err
	}

	var added []string
	var skipped []string
	for i, backend := range backends {
		name, _ := backend["name"].(string)
		if name == "" {
			return nil, nil, fmt.Errorf("spec.backends[%d] missing required name", i)
		}

		storageRaw, ok := backend["storage"]
		if !ok {
			skipped = append(skipped, name+" (no storage)")
			continue
		}
		storage, ok := storageRaw.(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("spec.backends[%d].storage is not an object", i)
		}
		enabled, found, err := unstructured.NestedBool(storage, "enabled")
		if err != nil {
			return nil, nil, fmt.Errorf("read spec.backends[%d].storage.enabled: %w", i, err)
		}
		if !found || !enabled {
			skipped = append(skipped, name+" (storage disabled)")
			continue
		}

		mounts, changed, err := ensureMounts(storage["mounts"], []runtimeMount{
			{SubPath: "state", MountPath: runtimeStatePath},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("read spec.backends[%d].storage.mounts: %w", i, err)
		}
		if changed {
			storage["mounts"] = mounts
			backend["storage"] = storage
			backends[i] = backend
			added = append(added, name)
		}
	}

	if len(added) > 0 {
		if err := writeBackends(cr, backends); err != nil {
			return nil, nil, err
		}
	}
	sort.Strings(added)
	sort.Strings(skipped)
	return added, skipped, nil
}

type runtimeMount struct {
	SubPath   string
	MountPath string
}

func ensureMounts(raw interface{}, required []runtimeMount) ([]interface{}, bool, error) {
	var mounts []interface{}
	switch v := raw.(type) {
	case nil:
		mounts = []interface{}{}
	case []interface{}:
		mounts = append([]interface{}{}, v...)
	default:
		return nil, false, fmt.Errorf("mounts is not a list; got %T", raw)
	}

	changed := false
	for _, req := range required {
		if mountListHasPath(mounts, req.MountPath) {
			continue
		}
		mounts = append(mounts, map[string]interface{}{
			"subPath":   req.SubPath,
			"mountPath": req.MountPath,
		})
		changed = true
	}
	return mounts, changed, nil
}

func mountListHasPath(mounts []interface{}, mountPath string) bool {
	for _, raw := range mounts {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if got, _ := m["mountPath"].(string); got == mountPath {
			return true
		}
	}
	return false
}

func runtimeStoragePlanValue(cr *unstructured.Unstructured, agentName string) string {
	existingClaim, _, _ := unstructured.NestedString(cr.Object, "spec", "runtimeStorage", "existingClaim")
	if existingClaim != "" {
		return "use existing claim " + existingClaim
	}
	size, _, _ := unstructured.NestedString(cr.Object, "spec", "runtimeStorage", "size")
	if size == "" {
		size = "1Gi"
	}
	return fmt.Sprintf("%s-runtime-data (%s)", agentName, size)
}

func waitForDeploymentRollout(ctx context.Context, k8sClient kubernetes.Interface, ns, name string, timeout time.Duration, out io.Writer) error {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		if time.Now().After(deadline) {
			fmt.Fprintf(out, "\nTimed out after %s — recent events for %s/%s:\n", timeout, ns, name)
			dumpRecentEvents(ctx, k8sClient, ns, name, out)
			return fmt.Errorf("timed out after %s waiting for deployment %q rollout", timeout, name)
		}

		deploy, err := k8sClient.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				status := "deployment not found yet"
				if status != last {
					fmt.Fprintf(out, "  rollout: %s\n", status)
					last = status
				}
			} else {
				fmt.Fprintf(out, "  (deployment get failed: %v; retrying)\n", err)
			}
		} else {
			desired := int32(1)
			if deploy.Spec.Replicas != nil {
				desired = *deploy.Spec.Replicas
			}
			status := fmt.Sprintf("updated=%d available=%d replicas=%d desired=%d",
				deploy.Status.UpdatedReplicas,
				deploy.Status.AvailableReplicas,
				deploy.Status.Replicas,
				desired,
			)
			if status != last {
				fmt.Fprintf(out, "  rollout: %s\n", status)
				last = status
			}
			if deploy.Status.ObservedGeneration >= deploy.Generation &&
				deploy.Status.UpdatedReplicas >= desired &&
				deploy.Status.AvailableReplicas >= desired &&
				deploy.Status.Replicas == deploy.Status.UpdatedReplicas {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return errors.New("wait cancelled")
		case <-time.After(2 * time.Second):
		}
	}
}
