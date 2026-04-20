/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
...
*/

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	witwavev1alpha1 "github.com/witwave-ai/witwave-operator/api/v1alpha1"
)

func TestDefaulterPopulatesPortWhenZero(t *testing.T) {
	d := &WitwaveAgentCustomDefaulter{}
	agent := &witwavev1alpha1.WitwaveAgent{}
	if err := d.Default(context.Background(), agent); err != nil {
		t.Fatalf("Default returned err: %v", err)
	}
	if agent.Spec.Port != 8000 {
		t.Fatalf("expected default port 8000, got %d", agent.Spec.Port)
	}
}

func TestDefaulterPreservesExplicitPort(t *testing.T) {
	d := &WitwaveAgentCustomDefaulter{}
	agent := &witwavev1alpha1.WitwaveAgent{Spec: witwavev1alpha1.WitwaveAgentSpec{Port: 9000}}
	if err := d.Default(context.Background(), agent); err != nil {
		t.Fatalf("Default returned err: %v", err)
	}
	if agent.Spec.Port != 9000 {
		t.Fatalf("expected preserved port 9000, got %d", agent.Spec.Port)
	}
}

func TestValidatorRejectsDuplicateBackendNames(t *testing.T) {
	v := &WitwaveAgentCustomValidator{}
	agent := &witwavev1alpha1.WitwaveAgent{
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Backends: []witwavev1alpha1.BackendSpec{
				{Name: "claude"},
				{Name: "codex"},
				{Name: "claude"}, // duplicate
			},
		},
	}
	_, err := v.ValidateCreate(context.Background(), agent)
	if err == nil {
		t.Fatal("expected error for duplicate backend name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("expected error to mention duplicates, got: %v", err)
	}
}

func TestValidatorAllowsUniqueBackendNames(t *testing.T) {
	v := &WitwaveAgentCustomValidator{}
	agent := &witwavev1alpha1.WitwaveAgent{
		Spec: witwavev1alpha1.WitwaveAgentSpec{
			Backends: []witwavev1alpha1.BackendSpec{
				{Name: "claude"},
				{Name: "codex"},
				{Name: "gemini"},
			},
		},
	}
	if _, err := v.ValidateCreate(context.Background(), agent); err != nil {
		t.Fatalf("ValidateCreate returned err: %v", err)
	}
	if _, err := v.ValidateUpdate(context.Background(), nil, agent); err != nil {
		t.Fatalf("ValidateUpdate returned err: %v", err)
	}
}
