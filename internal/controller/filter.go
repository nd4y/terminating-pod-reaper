package controller

import (
	"fmt"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// Filter decides whether a pod in a given namespace is subject to reaping.
//
// Namespace rules:
//   - if ExcludeRegex is set and the name matches         -> exclude;
//   - if ExcludeSelector is set and the ns labels match    -> exclude;
//   - if IncludeRegex is set and the name does NOT match   -> exclude;
//   - if IncludeSelector is set and the ns labels do NOT match -> exclude;
//   - otherwise -> include.
//
// Exclude takes priority over Include. Empty rules impose no restriction.
//
// Pod rules:
//   - if PodExcludeSelector is set and the pod labels match -> the pod is skipped;
//   - if OwnerKinds is set -> the pod is only processed if its controller-owner
//     has one of the listed Kinds (e.g. ReplicaSet, Job). Bare pods and
//     StatefulSet/DaemonSet pods are left alone.
type Filter struct {
	NsIncludeRe   *regexp.Regexp
	NsExcludeRe   *regexp.Regexp
	NsIncludeSel  labels.Selector
	NsExcludeSel  labels.Selector
	PodExcludeSel labels.Selector
	OwnerKinds    map[string]bool
}

// NeedsNamespaceLabels reports whether the Namespace object needs to be fetched for its labels.
func (f *Filter) NeedsNamespaceLabels() bool {
	return f.NsIncludeSel != nil || f.NsExcludeSel != nil
}

// NamespaceAllowed checks a namespace by name and by its labels.
func (f *Filter) NamespaceAllowed(name string, nsLabels map[string]string) bool {
	lbls := labels.Set(nsLabels)

	if f.NsExcludeRe != nil && f.NsExcludeRe.MatchString(name) {
		return false
	}
	if f.NsExcludeSel != nil && f.NsExcludeSel.Matches(lbls) {
		return false
	}
	if f.NsIncludeRe != nil && !f.NsIncludeRe.MatchString(name) {
		return false
	}
	if f.NsIncludeSel != nil && !f.NsIncludeSel.Matches(lbls) {
		return false
	}
	return true
}

// PodAllowed returns false if the pod is excluded by its own labels.
func (f *Filter) PodAllowed(podLabels map[string]string) bool {
	if f.PodExcludeSel != nil && f.PodExcludeSel.Matches(labels.Set(podLabels)) {
		return false
	}
	return true
}

// OwnerAllowed checks that the pod is managed by a controller of an allowed Kind.
// An empty OwnerKinds means no restriction. A bare pod (no controller-owner) with
// OwnerKinds set does not pass.
func (f *Filter) OwnerAllowed(refs []metav1.OwnerReference) bool {
	if len(f.OwnerKinds) == 0 {
		return true
	}
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return f.OwnerKinds[refs[i].Kind]
		}
	}
	return false
}

// BuildFilter assembles a Filter from string parameters (flags/env).
// Empty strings mean "rule not set".
func BuildFilter(
	nsIncludeRegex, nsExcludeRegex string,
	nsIncludeSelector, nsExcludeSelector string,
	podExcludeSelector string,
	ownerKinds string,
) (*Filter, error) {
	f := &Filter{}
	var err error

	if ownerKinds != "" {
		f.OwnerKinds = map[string]bool{}
		for _, k := range strings.Split(ownerKinds, ",") {
			if k = strings.TrimSpace(k); k != "" {
				f.OwnerKinds[k] = true
			}
		}
	}

	compileRe := func(name, expr string) (*regexp.Regexp, error) {
		if expr == "" {
			return nil, nil
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid regular expression %q: %w", name, expr, err)
		}
		return re, nil
	}
	parseSel := func(name, expr string) (labels.Selector, error) {
		if expr == "" {
			return nil, nil
		}
		sel, err := labels.Parse(expr)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid label selector %q: %w", name, expr, err)
		}
		return sel, nil
	}

	if f.NsIncludeRe, err = compileRe("namespace-include-regex", nsIncludeRegex); err != nil {
		return nil, err
	}
	if f.NsExcludeRe, err = compileRe("namespace-exclude-regex", nsExcludeRegex); err != nil {
		return nil, err
	}
	if f.NsIncludeSel, err = parseSel("namespace-include-selector", nsIncludeSelector); err != nil {
		return nil, err
	}
	if f.NsExcludeSel, err = parseSel("namespace-exclude-selector", nsExcludeSelector); err != nil {
		return nil, err
	}
	if f.PodExcludeSel, err = parseSel("pod-exclude-selector", podExcludeSelector); err != nil {
		return nil, err
	}
	return f, nil
}
