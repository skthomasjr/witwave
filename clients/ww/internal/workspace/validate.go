package workspace

import (
	"fmt"
	"regexp"
)

// Workspace name length cap. The CRD bounds the field via DNS-1123
// pattern + 63-char limit on the operator side; this matches.
const maxWorkspaceNameLen = 63

// DNS-1123 label pattern: lowercase alphanumerics and '-', must start and
// end with alphanumeric. Matches Kubernetes' own validation and the CRD's
// ^[a-z0-9][a-z0-9-]*$ pattern.
var dns1123LabelRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// ValidateName enforces the naming rules for Workspace CRs. Same shape
// as agent.ValidateName but bumped to 63 chars because Workspace names
// don't get container-name-derivation churn — the CRD itself accepts up
// to the DNS-1123 label maximum.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("workspace name must not be empty")
	}
	if len(name) > maxWorkspaceNameLen {
		return fmt.Errorf(
			"workspace name %q is %d characters; maximum is %d (DNS-1123 label limit)",
			name, len(name), maxWorkspaceNameLen,
		)
	}
	if !dns1123LabelRE.MatchString(name) {
		return fmt.Errorf(
			"workspace name %q is invalid: must be lowercase alphanumerics and '-' only, start and end with alphanumeric (DNS-1123 label)",
			name,
		)
	}
	return nil
}

// ValidateVolumeName enforces the per-volume name shape declared in the
// Workspace CRD's WorkspaceVolume.Name field.
func ValidateVolumeName(name string) error {
	if name == "" {
		return fmt.Errorf("volume name must not be empty")
	}
	if len(name) > 63 {
		return fmt.Errorf(
			"volume name %q is %d characters; maximum is 63 (DNS-1123 label limit)",
			name, len(name),
		)
	}
	if !dns1123LabelRE.MatchString(name) {
		return fmt.Errorf(
			"volume name %q is invalid: must be lowercase alphanumerics and '-' only, start and end with alphanumeric (DNS-1123 label)",
			name,
		)
	}
	return nil
}
