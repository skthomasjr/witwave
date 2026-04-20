# Migration

CRD version transitions for `witwave.ai/v1alpha1` → `witwave.ai/v1beta1`
and beyond. Operator release notes call out the relevant section;
this document is the authoritative reference for upgrade paths.

Pre-1.0 the project is in `v1alpha1` and backwards-incompatible
changes can land without a version bump — see the **Deprecation
policy** section below for the contract we'll follow once `v1beta1`
arrives. Today nothing migratory is required; this page exists so
the first CRD version transition doesn't surprise anyone.

---

## Deprecation policy

We commit to the following once multi-version CRDs start shipping:

### Served versions

A CRD version remains `served: true` for at least **two full minor
releases** after its successor is introduced as the storage version.
Concretely: if `v1beta1` is introduced as the stored version in ww
`v0.7.0`, `v1alpha1` remains served through `v0.7.x` and `v0.8.x`;
`v0.9.0` is the earliest release that can drop `v1alpha1`.

During the overlap window, both versions appear in
`kubectl api-versions` and the operator accepts CRs written to
either. The conversion webhook rewrites reads to the storage version;
clients written against the old version continue to work without
modification.

### Storage version

One storage version per CRD at any time. The storage-version switch
requires a conversion webhook (see below). The switch is performed
in a dedicated release — never combined with a conversion-webhook
behavioural change. This keeps the rollback lever single-axis.

### CHANGELOG.md entries

Every CRD deprecation lands in CHANGELOG.md under **Changed** with
the specific version going served=false + the target release for
`served=false`. No silent dropping.

---

## Conversion webhook architecture

### Scheme

`v1alpha1` types are marked `// +kubebuilder:object:root=true` and
`// +kubebuilder:storageversion` today. When `v1beta1` lands, the
storageversion marker moves to `v1beta1`; `v1alpha1` loses the
marker but keeps the `served=true` flag through the deprecation
window.

Both versions are registered in `api/v1alpha1/groupversion_info.go`
and `api/v1beta1/groupversion_info.go`. The conversion webhook
lives in the operator binary — controller-runtime exposes the
`webhook.Webhook` interface; we implement `Convertible.ConvertTo` /
`ConvertFrom` on each versioned type.

### Certificate management

The webhook uses the operator's existing cert setup —
`operator/config/certmanager/` renders a `Certificate` CR that
cert-manager signs, mounted at `/tmp/k8s-webhook-server/serving-certs`
on the operator pod. No additional cert plumbing needed beyond what
the validating / mutating webhooks already use.

**For clusters without cert-manager**, the chart ships a
pre-generated self-signed certificate via
`charts/witwave-operator/templates/certificate-self-signed.yaml`
(gated by `webhook.certManagerEnabled: false`). This path is
documented but not the recommended production posture.

### Webhook configuration

`CustomResourceConversion.strategy: Webhook` on the CRD spec.
Rendered by the operator at install time (via `make manifests`)
into `config/crd/bases/*.yaml`. The chart's `crds/` directory
ships the same files.

---

## Upgrade paths

### `v1alpha1` → `v1beta1` (not yet available)

When `v1beta1` ships, the upgrade path is:

1. **Upgrade the operator first.** `ww operator upgrade` — this
   installs the conversion webhook and registers `v1beta1` as
   served + stored.
2. **Verify both versions are served.**
   ```bash
   kubectl api-versions | grep witwave.ai
   # Expected:
   #   witwave.ai/v1alpha1
   #   witwave.ai/v1beta1
   ```
3. **Migrate existing CRs to `v1beta1` (optional but recommended).**
   The conversion webhook handles `v1alpha1` reads transparently, so
   existing YAML keeps working. But rewriting them to `v1beta1` now
   means less reconciliation churn when `v1alpha1` is eventually
   dropped. One-liner:
   ```bash
   kubectl get witwaveagents.v1beta1.witwave.ai -A -o yaml > witwaveagents.yaml
   kubectl apply -f witwaveagents.yaml
   ```
4. **Monitor the operator for conversion errors.** Anomalous
   `operator/webhook/conversion_errors_total` counts point at a
   schema drift the conversion path didn't cover — file a bug.

### Manual conversion fallback

Clusters that can't install a conversion webhook (e.g. clusters
without cert-manager AND with `webhook.certManagerEnabled: false`
explicitly rejected) can convert CRs manually:

```bash
# Dump all v1alpha1 CRs.
kubectl get witwaveagents.v1alpha1.witwave.ai -A -o json > v1alpha1.json

# Apply a jq transform to rename deprecated fields (the exact
# transform ships with each v1beta1 release as
# scripts/migrate-v1alpha1-to-v1beta1.jq).
jq -f scripts/migrate-v1alpha1-to-v1beta1.jq v1alpha1.json > v1beta1.json

# Apply the converted CRs. Re-applies are idempotent; the webhook
# isn't involved when you're already writing the storage version.
kubectl apply -f v1beta1.json
```

The jq transform file will be provided alongside each version
release — it's not generated automatically from the Go types but
it IS the source of truth for the server-side conversion webhook's
logic (tests in `internal/webhook/conversion_test.go` round-trip
between the jq output and the webhook's `ConvertTo` / `ConvertFrom`
results).

---

## Test plan

Every CRD version transition ships with the following minimum test
matrix in `internal/webhook/conversion_test.go`:

| Source version | Storage version | Target version | Expected result |
|----------------|-----------------|----------------|-----------------|
| `v1alpha1`     | `v1beta1`       | `v1alpha1`     | Round-trip OK   |
| `v1alpha1`     | `v1beta1`       | `v1beta1`      | Field-exact     |
| `v1beta1`      | `v1beta1`       | `v1alpha1`     | Lossy fields dropped with warning |
| `v1beta1`      | `v1beta1`       | `v1beta1`      | Identity        |

Lossy round-trips (reading `v1beta1` back as `v1alpha1`) may drop
fields that didn't exist in the older version. The webhook logs a
one-shot WARN per lossy field per reconcile so operators see what
they're losing. The jq transform mirrors this behaviour by emitting
`.deprecated: true` annotations on affected CRs.

---

## What this document is NOT

- Not a general CRD design guide. kubebuilder's book covers that.
- Not a rollback procedure. `kubectl apply -f` the previous version
  and `ww operator upgrade --chart-version <previous>` is the
  rollback. Downgrades across a storage-version switch require
  exporting CRs as the older version first — the same jq transform
  applied in reverse.

---

## Status

`v1alpha1` is the only served + stored version today. The policy
above is a commitment for when `v1beta1` lands; the conversion
webhook code is scaffolded but not yet wired into the operator's
webhook-server startup. When `v1beta1` begins, this page is the
first place it will be announced.

Questions about migrations before `v1beta1` ships should go to
GitHub Issues with the `operator` label.
