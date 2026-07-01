package controller

import (
	"fmt"
	"regexp"

	"k8s.io/apimachinery/pkg/labels"
)

// Filter решает, подлежит ли под в данном namespace обработке (reaping).
//
// Правила namespace:
//   - если задан ExcludeRegex и имя совпало     → исключить;
//   - если задан ExcludeSelector и метки ns совпали → исключить;
//   - если задан IncludeRegex и имя НЕ совпало   → исключить;
//   - если задан IncludeSelector и метки ns НЕ совпали → исключить;
//   - иначе → включить.
//
// Exclude имеет приоритет над Include. Пустые правила ничего не ограничивают.
//
// Правило подов:
//   - если задан PodExcludeSelector и метки пода совпали → под пропускается.
type Filter struct {
	NsIncludeRe   *regexp.Regexp
	NsExcludeRe   *regexp.Regexp
	NsIncludeSel  labels.Selector
	NsExcludeSel  labels.Selector
	PodExcludeSel labels.Selector
}

// NeedsNamespaceLabels — нужно ли подтягивать объект Namespace ради его меток.
func (f *Filter) NeedsNamespaceLabels() bool {
	return f.NsIncludeSel != nil || f.NsExcludeSel != nil
}

// NamespaceAllowed проверяет namespace по имени и его меткам.
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

// PodAllowed возвращает false, если под исключён по своим меткам.
func (f *Filter) PodAllowed(podLabels map[string]string) bool {
	if f.PodExcludeSel != nil && f.PodExcludeSel.Matches(labels.Set(podLabels)) {
		return false
	}
	return true
}

// BuildFilter собирает Filter из строковых параметров (флаги/env).
// Пустые строки означают «правило не задано».
func BuildFilter(
	nsIncludeRegex, nsExcludeRegex string,
	nsIncludeSelector, nsExcludeSelector string,
	podExcludeSelector string,
) (*Filter, error) {
	f := &Filter{}
	var err error

	compileRe := func(name, expr string) (*regexp.Regexp, error) {
		if expr == "" {
			return nil, nil
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("%s: неверное регулярное выражение %q: %w", name, expr, err)
		}
		return re, nil
	}
	parseSel := func(name, expr string) (labels.Selector, error) {
		if expr == "" {
			return nil, nil
		}
		sel, err := labels.Parse(expr)
		if err != nil {
			return nil, fmt.Errorf("%s: неверный label selector %q: %w", name, expr, err)
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
