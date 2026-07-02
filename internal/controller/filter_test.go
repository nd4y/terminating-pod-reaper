package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mustFilter(t *testing.T, incRe, excRe, incSel, excSel, podSel string) *Filter {
	t.Helper()
	f, err := BuildFilter(incRe, excRe, incSel, excSel, podSel, "")
	if err != nil {
		t.Fatalf("BuildFilter: %v", err)
	}
	return f
}

func TestNamespaceAllowed(t *testing.T) {
	tests := []struct {
		name                                   string
		incRe, excRe, incSel, excSel           string
		ns                                     string
		nsLabels                               map[string]string
		want                                   bool
	}{
		{name: "no rules → allow", ns: "anything", want: true},
		{name: "include regex match", incRe: "^app-", ns: "app-prod", want: true},
		{name: "include regex no match", incRe: "^app-", ns: "kube-system", want: false},
		{name: "exclude regex match", excRe: "^kube-", ns: "kube-system", want: false},
		{name: "exclude regex no match", excRe: "^kube-", ns: "app-prod", want: true},
		{name: "exclude beats include", incRe: "prod", excRe: "^app-", ns: "app-prod", want: false},
		{
			name: "include selector match", incSel: "terminating-pod-reaper=enabled",
			ns: "app-prod", nsLabels: map[string]string{"terminating-pod-reaper": "enabled"}, want: true,
		},
		{
			name: "include selector no match", incSel: "terminating-pod-reaper=enabled",
			ns: "app-prod", nsLabels: map[string]string{"terminating-pod-reaper": "off"}, want: false,
		},
		{
			name: "exclude selector match", excSel: "protected=true",
			ns: "db", nsLabels: map[string]string{"protected": "true"}, want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := mustFilter(t, tc.incRe, tc.excRe, tc.incSel, tc.excSel, "")
			if got := f.NamespaceAllowed(tc.ns, tc.nsLabels); got != tc.want {
				t.Fatalf("NamespaceAllowed(%q,%v)=%v, want %v", tc.ns, tc.nsLabels, got, tc.want)
			}
		})
	}
}

func TestPodAllowed(t *testing.T) {
	f := mustFilter(t, "", "", "", "", "terminating-pod-reaper.io/ignore=true")
	if !f.PodAllowed(map[string]string{"app": "web"}) {
		t.Fatal("под без ignore-метки должен обрабатываться")
	}
	if f.PodAllowed(map[string]string{"terminating-pod-reaper.io/ignore": "true"}) {
		t.Fatal("под с ignore-меткой должен быть пропущен")
	}
}

func TestNeedsNamespaceLabels(t *testing.T) {
	if mustFilter(t, "^app-", "", "", "", "").NeedsNamespaceLabels() {
		t.Fatal("regex-only фильтр не должен требовать метки namespace")
	}
	if !mustFilter(t, "", "", "terminating-pod-reaper=enabled", "", "").NeedsNamespaceLabels() {
		t.Fatal("selector-фильтр должен требовать метки namespace")
	}
}

func TestBuildFilterErrors(t *testing.T) {
	if _, err := BuildFilter("(", "", "", "", "", ""); err == nil {
		t.Fatal("ожидалась ошибка на кривой regex")
	}
	if _, err := BuildFilter("", "", "!!bad", "", "", ""); err == nil {
		t.Fatal("ожидалась ошибка на кривом label selector")
	}
}

func ctrlRef(kind string) metav1.OwnerReference {
	yes := true
	return metav1.OwnerReference{Kind: kind, Controller: &yes}
}

func TestOwnerAllowed(t *testing.T) {
	f, err := BuildFilter("", "", "", "", "", "ReplicaSet,Job")
	if err != nil {
		t.Fatalf("BuildFilter: %v", err)
	}

	tests := []struct {
		name string
		refs []metav1.OwnerReference
		want bool
	}{
		{"ReplicaSet (Deployment)", []metav1.OwnerReference{ctrlRef("ReplicaSet")}, true},
		{"Job (CronJob)", []metav1.OwnerReference{ctrlRef("Job")}, true},
		{"StatefulSet", []metav1.OwnerReference{ctrlRef("StatefulSet")}, false},
		{"DaemonSet", []metav1.OwnerReference{ctrlRef("DaemonSet")}, false},
		{"голый под без владельца", nil, false},
		{"owner без controller=true", []metav1.OwnerReference{{Kind: "ReplicaSet"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.OwnerAllowed(tc.refs); got != tc.want {
				t.Fatalf("OwnerAllowed(%s)=%v, want %v", tc.name, got, tc.want)
			}
		})
	}

	// Пустой список Kind → ограничения нет.
	all := mustFilter(t, "", "", "", "", "")
	if !all.OwnerAllowed(nil) {
		t.Fatal("при пустом OwnerKinds должен проходить любой под")
	}
}
