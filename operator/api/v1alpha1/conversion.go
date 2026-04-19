/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Conversion-webhook scaffold (#833).
//
// v1alpha1 is currently the only served version of both CRDs in this
// module. Marking it explicitly as the **conversion Hub** via the
// “Hub()“ method lands the kubebuilder-style scaffold *now*, so the
// next version bump (v1beta1, v1, …) only needs to:
//
//  1. Add the new API types under “api/v<new>/“.
//  2. Implement “ConvertTo(dst conversion.Hub)“ and
//     “ConvertFrom(src conversion.Hub)“ on the new types, with
//     identity field copies for anything that has not actually
//     changed shape.
//  3. Turn on the conversion webhook in the CRD manifests (see the
//     “[WEBHOOK]“ patch blocks in “operator/config/crd/“).
//
// Shipping “Hub()“ alone has no runtime effect on a single-version
// deployment (controller-runtime only invokes conversion when two or
// more versions are served), but it is the canonical kubebuilder
// pattern that unlocks a zero-downtime version bump without a
// migration script.
//
// Both “DashboardSpec.HarnessURL“ and similar fields already carry
// “// Deprecated:“ doc comments, foreshadowing a future shape change;
// landing the scaffold now means the first breaking field removal can
// ship with an identity-plus-drop conversion instead of forcing every
// existing CR to be re-applied by hand.
package v1alpha1

// Hub marks NyxAgent v1alpha1 as the conversion hub. Required signature
// for sigs.k8s.io/controller-runtime's conversion machinery.
func (*NyxAgent) Hub() {}

// Hub marks NyxPrompt v1alpha1 as the conversion hub.
func (*NyxPrompt) Hub() {}
