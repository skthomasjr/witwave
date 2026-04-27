# Security Policy

This project runs LLM-generated code inside Kubernetes clusters with access to the apiserver, Secrets, the pod network,
and webhooks reaching external systems. Security reports are taken seriously. This document describes how to get one to
us and what we consider in scope.

## Reporting a vulnerability

Email: **<security@witwave.ai>**

Please do **not** open a public GitHub issue for a suspected vulnerability. If email is inconvenient, GitHub's private
vulnerability reporting (on the repository's **Security** tab → "Report a vulnerability") works too.

A useful report includes:

- Affected version — commit SHA, chart version, or image tag
- A minimal reproducer or enough detail that a maintainer can recreate it
- Your assessment of impact (auth bypass, privilege escalation, information disclosure, etc.)
- A suggested fix, if you have one in mind

Reports in English preferred; other languages will slow us down but won't stop us.

## What to expect after you report

This is a small project. We'll aim to acknowledge your report quickly and keep you updated as we investigate. Complex
issues may take longer than simple ones — we'd rather give you an honest timeline than miss one we already promised.

If your report turns out to be out of scope (see below), we'll tell you why; it's not a brush-off.

## Supported versions

The project is pre-1.0. Security fixes land on `main` and flow into the next tagged release. We don't backport to prior
tags.

| Version    | Gets fixes?                              |
| ---------- | ---------------------------------------- |
| `main`     | Yes                                      |
| Latest tag | Typically yes, when a fix release is cut |
| Older tags | No — upgrade to the latest               |

## In scope

Issues we consider in scope (non-exhaustive):

- **Auth bypass** on protected endpoints — `/conversations`, `/trace`, `/mcp`, `/api/traces[/...]`, `/events/stream`,
  `/api/sessions/*/stream`, harness trigger endpoints, ad-hoc-run endpoints listed under `/.well-known/agent-runs.json`.
- **Bearer-token exposure** in logs, metrics, events, persisted JSONL, or anywhere else a scrape/collector might pick it
  up.
- **Session-ID hijacking** across caller identities. When `SESSION_ID_SECRET` is set,
  `shared/session_binding.derive_session_id` HMACs session IDs to the caller's bearer fingerprint; a bypass or downgrade
  is in scope.
- **MCP command allow-list bypass** — invoking shell commands outside `MCP_ALLOWED_COMMANDS` /
  `MCP_ALLOWED_COMMAND_PREFIXES` / `MCP_ALLOWED_CWD_PREFIXES`, or coaxing the allow-list into accepting something it
  shouldn't.
- **Operator privilege escalation** — a CR-driven write reaching a kind, namespace, or Secret outside the documented
  RBAC scope (see `charts/witwave-operator/values.yaml` `rbac.*` keys).
- **SSRF in webhook delivery** — reaching in-cluster or localhost services via URL shapes the allow-list
  (`WEBHOOK_ALLOW_LOOPBACK_HOSTS`, scheme/host/port checks) didn't anticipate.
- **Redaction bypass** — sensitive values surviving the `shared/redact.py` pipeline into logs, events, or trace
  attributes. The redaction guarantee is idempotent merge-spans; a counterexample is a bug.
- **Traceparent / session-ID injection** letting a caller set or forge another agent's trace context or session
  identity.
- **Server-Side Apply regressions** where a reconciler write overwrites a human-owned field on a Secret, ConfigMap, or
  other kind labelled `app.kubernetes.io/component: credentials`.

## Out of scope

- **LLM jailbreaks** that make the backend produce certain text. The model's content output is not a security boundary.
  Hook policies and redaction are defenses in depth, not perimeter controls.
- **Denial-of-service by an already-authorized caller.** Authorized callers can always degrade their own agent's
  availability. If the DoS vector reaches unauthorized callers, that's in scope.
- **Issues that require cluster-admin to begin with.** If the attacker already has cluster-admin on the cluster, they
  don't need this platform.
- **Third-party dependency CVEs** with existing upstream advisories — track those upstream; we'll pick up fixes through
  normal dependency updates.
- **Missing security headers on non-auth-gated paths** that don't carry sensitive data (the bare `/health` and
  `/metrics` listeners, assuming `METRICS_ENABLED` is off on public networks — which it should be).

## Known-hazardous areas (designed-in risks, not bugs)

A few risks are inherent to how the platform works. Reporting these as bugs is out of scope, but understanding them is
useful context:

- **MCP tool containers execute LLM-generated commands.** The allow-lists in `shared/mcp_auth.py` and per-tool envs are
  narrow by default, but they are _policy_, not _proof_. Binding any MCP tool to `cluster-admin` is a footgun and the
  per-tool READMEs (`tools/*/README.md`) call it out.
- **Hook policies** (`.claude/hooks.yaml`, the engine in `backends/claude/hooks.py`) are a filtering and observability
  layer. They are useful, but they are not a security boundary — an LLM that wants to exfiltrate via a hook-allowed path
  likely can. Design your auth and RBAC assuming hook policy may leak.
- **Local-dev escape hatches** — `CONVERSATIONS_AUTH_DISABLED=true` and `MCP_TOOL_AUTH_DISABLED=true` exist so operators
  don't invent worse bypasses. Both log loud startup warnings. They are intentional, not bugs.

## Coordinated disclosure

We prefer coordinated disclosure and will work with you on a timeline that gives us room to fix the issue before details
become public. Typical industry practice is around 90 days from initial acknowledgment, but we don't treat that as a
contract — complex fixes may warrant more time, and obvious ones less. We'll talk.

## Working with us

Good-faith security research on your own deployment, or on deliberately-exposed test infrastructure, is welcome. Don't
exfiltrate real user data, don't persist unauthorized access once you've confirmed a finding, and don't degrade
production systems you don't own. If you're uncertain whether your planned research is in bounds, email us before you
start — we'd rather answer a preflight question than interpret a fait accompli.

This isn't a legal safe-harbor contract; it's a description of how we'd like to work with researchers.

## Credit

We're happy to credit reporters in release notes or commit trailers. Tell us how you'd like to be listed — by name,
handle, or anonymously.

## Bug bounty

None. The project is pre-1.0 and privately funded. We can offer credit, gratitude, and a fix that benefits everyone
running the platform. That's what we've got.

## Verifying signed release artefacts

### Container images

Every image published under `ghcr.io/witwave-ai/images/*` on a tag release is cosign-signed via Sigstore's keyless
(OIDC) flow (#1460). No long-lived signing key lives in this repo — the certificate identity is the release workflow
itself, bound to the tag the image was built from.

To verify an image before running it:

```bash
# Note: image tags strip the leading "v" — the git tag v0.9.6 pushes
# images tagged 0.9.6 (docker/metadata-action@v5 default semver
# normalisation). Helm chart tags also strip the v. `latest` and
# `<major>.<minor>` aliases exist as well.
IMAGE=ghcr.io/witwave-ai/images/operator:0.9.6

cosign verify \
  --certificate-identity-regexp="^https://github.com/witwave-ai/witwave/\.github/workflows/release\.yaml@refs/tags/v.*$" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  "$IMAGE"
```

Expected output: a JSON payload echoing the signing certificate's identity + Rekor log index. Any of the following mean
**do not run the image**:

- Non-zero exit — the signature doesn't verify, the cert identity doesn't match, or Rekor has no record.
- `no matching signatures` — image was pushed without a signature (e.g. a dev build, a pre-release tag before #1460
  shipped, or a compromise that swapped the image without updating the signature).
- `certificate verification failure` — the signing identity isn't our release workflow; refuse.

### Image provenance + SBOM attestations

Every image also ships with **SLSA build provenance** and a **SPDX SBOM** as buildx attestations attached to the OCI
image index (#1598 item 1, since v0.9.3). Unlike the cosign signature above (which proves "this image came from our
release workflow"), the attestations describe **what was built** — full build inputs, source revision, every package
baked into the image.

Inspect via `docker buildx imagetools inspect` (the cosign-verify-attestation flow does NOT find these — buildx attaches
attestations to the image index, not in cosign's `<digest>.att` location):

```bash
# Provenance — full SLSA build predicate
docker buildx imagetools inspect "$IMAGE" --format '{{json .Provenance}}'

# SBOM — every package and version baked into the image
docker buildx imagetools inspect "$IMAGE" --format '{{json .SBOM}}'
```

The attestations are unsigned (buildx's default mode); the cosign signature above is what you trust to bind the
attestations to the image. Verify the image first, then trust whatever the inspect commands reveal.

### Cluster-side enforcement (optional)

Running a verifying admission controller — Sigstore's
[policy-controller](https://docs.sigstore.dev/policy-controller/overview/) or
[Kyverno](https://kyverno.io/policies/cleanup/cleanup-sigstore-verify-images/) — makes the check happen automatically at
pod schedule time and refuses unsigned images cluster-wide. The witwave-operator chart doesn't ship such a policy today;
it's a follow-up when demand materialises. For now, verification is a consumer-opt-in step.

### `ww` CLI binaries

Three install paths, three verification postures:

- **Homebrew** — verified via the tap's signature chain; nothing for the user to do.
- **curl installer** (`scripts/install.sh`, shipped as a release asset) — verifies the SHA256 of the downloaded
  tarball against `checksums.txt` from the same release, by default. Pass `--verify-signature` (or set
  `WW_VERIFY_SIGNATURE=1`) to additionally verify the cosign keyless signature on `checksums.txt`; requires `cosign`
  on PATH.
- **Direct binary download** (manual GitHub Releases pull) — no verification happens unless you do it. Run:

  ```bash
  cosign verify-blob \
    --certificate-identity-regexp="^https://github.com/witwave-ai/witwave/\.github/workflows/release-ww\.yml@refs/tags/v.*$" \
    --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
    --bundle ww_v0.9.6_darwin_arm64.tar.gz.cosign.bundle \
    ww_v0.9.6_darwin_arm64.tar.gz
  ```

  The `.cosign.bundle` file is published alongside each release asset.

### `ww` CLI SLSA provenance

Each release also ships a **SLSA L3 in-toto provenance predicate** as a `.intoto.jsonl` asset (#1598 item 2, since
v0.9.6). Generated by [`slsa-framework/slsa-github-generator`](https://github.com/slsa-framework/slsa-github-generator)
running as a separate isolated job — same path FluxCD, Helm, kubectl, and other CNCF projects use for their CLI binaries.

Verify via [`slsa-verifier`](https://github.com/slsa-framework/slsa-verifier):

```bash
# Install slsa-verifier — go install or grab the binary from
# github.com/slsa-framework/slsa-verifier/releases.

slsa-verifier verify-artifact \
  --provenance-path ww-v0.9.6.intoto.jsonl \
  --source-uri github.com/witwave-ai/witwave \
  --source-tag v0.9.6 \
  ww_0.9.6_darwin_arm64.tar.gz
```

Expected output: `PASSED: SLSA verification passed`. Anything else: refuse the binary.

The predicate's `subjects` field lists every release archive (one per platform) **plus** an embedded-chart content-hash
named `embedded-witwave-operator-<version>-content` — see "Embedded chart bridge" below.

### Helm charts

Charts published to `oci://ghcr.io/witwave-ai/charts/*` are signed at push time. Verify via:

```bash
cosign verify \
  --certificate-identity-regexp="^https://github.com/witwave-ai/witwave/\.github/workflows/release-helm\.yml@refs/tags/v.*$" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  oci://ghcr.io/witwave-ai/charts/witwave-operator:0.9.6
```

### Embedded chart bridge

The `ww` CLI **embeds** the witwave-operator helm chart so `ww operator install` deploys the operator without pulling
from any registry. The embedded chart is mirrored from `charts/witwave-operator/` via
`scripts/sync-embedded-chart.sh` at build time.

#1598 item 4 (since v0.9.6) provides a cryptographic bridge proving "the chart bytes the `ww` binary at this version
would deploy are byte-identical to the cosign-signed OCI chart at `ghcr.io/witwave-ai/charts/witwave-operator:<version>`."
Verification is two-step:

```bash
# 1. Pull and extract the published chart.
helm pull oci://ghcr.io/witwave-ai/charts/witwave-operator \
  --version 0.9.6 --untar --untardir /tmp/extracted

# 2. Compute its content-hash (the same recipe release-ww.yml ran
#    against the embedded copy at build time):
cd /tmp/extracted/witwave-operator
computed=$(find . -type f | LC_ALL=C sort | xargs sha256sum | sha256sum | awk '{print $1}')
echo "$computed"

# 3. Inspect the ww binary's SLSA predicate for the matching subject.
#    `slsa-verifier verify-artifact` (above) prints subjects on success;
#    or grep the .intoto.jsonl directly:
jq -r '.predicate.subject[]
  | select(.name == "embedded-witwave-operator-0.9.6-content")
  | .digest.sha256' \
  ww-v0.9.6.intoto.jsonl
```

If the two values match, the chart `ww operator install` would deploy at this version is provably the same as the OCI
chart. If they don't match: the embedded chart drifted from the published chart at release time — refuse to install.

### What signing does NOT prove

Signatures certify **provenance** (this image was built by our release workflow on this specific tag), not safety. A
signed image can still ship a bug, a vulnerability, or a compromised dependency that was in the source tree at build
time. Verification only tells you the bits came from us; whether the bits are _correct_ is a separate question that
scanning + code review answer.

## Token + secret rotation

### `HOMEBREW_TAP_GITHUB_TOKEN` — `ww` release-to-tap PAT

**Scope.** Fine-grained PAT on the [witwave-ai/homebrew-ww](https://github.com/witwave-ai/homebrew-ww) tap repository.
Minimum permissions: **Contents: Read and Write**. No other scopes — do NOT grant Administration, Pull Requests,
Secrets, or any other verbs.

**Where it lives.** Organization-level secret on `witwave-ai` (the release-source org) referenced as
`secrets.HOMEBREW_TAP_GITHUB_TOKEN` by `.github/workflows/release-ww.yml`. The workflow is hard-gated on
`github.ref_type == 'tag'` (see #1378) so the token is unreachable from `pull_request` / `workflow_dispatch` / forked
contributor runs.

**Rotation cadence.** **90 days**, or immediately on any of:

- Release workflow returns 401 or 403 on the tap push step.
- Token appears in a workflow log (should never happen — the PAT is masked — but if it does, rotate anyway).
- The person who generated the PAT leaves the project.

**Rotation procedure.**

1. Generate a new fine-grained PAT at <https://github.com/settings/personal-access-tokens> with scope **Contents: Read
   and Write on `witwave-ai/homebrew-ww`** and a 90-day expiry. Set the resource owner to `witwave-ai`.
2. Update `HOMEBREW_TAP_GITHUB_TOKEN` on the `witwave-ai` org secrets (GitHub Settings → Organizations → witwave-ai →
   Secrets and variables → Actions). Paste the new token value.
3. Trigger a dry-run of the release path — easiest is to cut a throwaway `v*.*.*-rc.*` tag (matches the release-ww
   workflow trigger), verify the tap push succeeds, then delete the tag + release.
4. Revoke the previous PAT from the original generator's PAT page. Don't wait for it to expire.

**Long-term replacement.** Fine-grained PATs are still tied to one person's GitHub identity. A GitHub App installation
on `witwave-ai/homebrew-ww` with `contents:write` and OIDC federation to the release workflow would remove the
human-in-the-loop. Tracked informally; file an issue when the human-PAT model actually bites.

### `SESSION_ID_SECRET` — MCP session-ID binding

**What it does.** `shared/session_binding.derive_session_id` HMAC-binds each `/mcp` session-id to the caller's
bearer-token fingerprint using `SESSION_ID_SECRET`. Two callers presenting the same raw `session_id` land in disjoint
sessions; a compromised session-id alone is useless without the original caller's token.

**Why rotate.** Defense-in-depth against a leaked secret (logs, env dumps, backup snapshots). The rotation mechanism
exists; this section documents the operator-facing procedure.

**Two-secret grace window.** The shared binding helper reads `SESSION_ID_SECRET` (current) and `SESSION_ID_SECRET_PREV`
(previous). On the **write** path it always uses the current secret to derive new IDs. On the **read** path it probes
`[current, prev]` and emits a one-shot WARN log per process when it gets a prev-secret hit — so operators can tell when
the grace window has drained.

**Observability signal.** A WARN log fires once per process on first prev-secret hit so operators know when traffic is
still resuming against the old secret. During rotation you'll see these warnings, then they'll stop as long-lived
sessions finish. When no pod has warned for at least the longest plausible session lifetime, `SESSION_ID_SECRET_PREV` is
safe to drop.

**Rotation procedure.**

1. Generate a new random secret — 32+ bytes from a cryptographic RNG (`openssl rand -base64 32` is fine). Do NOT reuse a
   secret from another system.
2. In every pod that mounts the MCP session secret (typically the harness + each backend), set `SESSION_ID_SECRET_PREV`
   to the CURRENT value of `SESSION_ID_SECRET`. Apply, roll pods.
3. After the pods are all re-reading the prev secret, set `SESSION_ID_SECRET` to the NEW secret. Apply, roll pods.
4. Monitor for "prev secret hit" WARN logs across the fleet — they fire once per process so a fresh spike after the
   rollout is expected and decays as long-lived sessions finish.
5. When the prev-hit warnings have been silent for longer than any plausible session could last (err on the side of
   longer — 24 hours is a reasonable default for interactive agent workloads), unset `SESSION_ID_SECRET_PREV`. Apply,
   roll pods. Rotation done.

**Cadence.** No fixed cadence. Rotate on:

- Suspicion the secret leaked (log dump, repo push of an env file, former-maintainer departure with access).
- Major version bump where you want a clean break of session-id derivation.

**Local-dev note.** Unset `SESSION_ID_SECRET` is the development default — session IDs are then HMAC'd with an empty
string (i.e. effectively unbound). Don't ship production with it unset.
