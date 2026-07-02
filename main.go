package main

import (
	"flag"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/nd4y/terminating-pod-reaper/internal/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
}

// envStr возвращает значение env, если оно непустое, иначе — переданный дефолт.
func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	var (
		metricsAddr    string
		probeAddr      string
		enableLeaderEl bool
		dryRun         bool
		namespacesFlag string

		nsIncludeRegex     string
		nsExcludeRegex     string
		nsIncludeSelector  string
		nsExcludeSelector  string
		podExcludeSelector string
		ownerKinds         string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Адрес для /metrics.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Адрес для health/ready проб.")
	flag.BoolVar(&enableLeaderEl, "leader-elect", false, "Включить leader election для HA (2+ реплик).")
	flag.BoolVar(&dryRun, "dry-run", true,
		"Только логировать, ничего не удалять. По умолчанию true (безопасный режим).")
	flag.StringVar(&namespacesFlag, "namespaces", "",
		"Жёсткое ограничение watch списком namespace через запятую. Пусто = весь кластер.")
	flag.StringVar(&nsIncludeRegex, "namespace-include-regex", "",
		"Обрабатывать только namespace, чьё имя совпадает с regex.")
	flag.StringVar(&nsExcludeRegex, "namespace-exclude-regex", "^kube-system$",
		"Исключить namespace, чьё имя совпадает с regex. По умолчанию защищён kube-system.")
	flag.StringVar(&nsIncludeSelector, "namespace-include-selector", "",
		"Обрабатывать только namespace с метками, совпадающими с label selector (напр. 'terminating-pod-reaper=enabled').")
	flag.StringVar(&nsExcludeSelector, "namespace-exclude-selector", "",
		"Исключить namespace с метками, совпадающими с label selector.")
	flag.StringVar(&podExcludeSelector, "pod-exclude-selector", "",
		"Не трогать поды с метками, совпадающими с label selector (напр. 'terminating-pod-reaper.io/ignore=true').")
	flag.StringVar(&ownerKinds, "reap-owner-kinds", "ReplicaSet,Job",
		"Удалять только поды под управлением контроллера с одним из Kind (через запятую). "+
			"Пусто = любой владелец, включая StatefulSet и «голые» поды.")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	// Env имеет приоритет над дефолтами флагов (удобно для деплоя через манифест).
	// Для DRY_RUN и параметров с непустым дефолтом используем LookupEnv: пустое
	// значение env значимо и должно перекрывать дефолт (чтобы Helm был авторитетен).
	if v, ok := os.LookupEnv("DRY_RUN"); ok {
		dryRun = v == "true"
	}
	namespacesFlag = envStr("NAMESPACES", namespacesFlag)
	nsIncludeRegex = envStr("NAMESPACE_INCLUDE_REGEX", nsIncludeRegex)
	if v, ok := os.LookupEnv("NAMESPACE_EXCLUDE_REGEX"); ok {
		nsExcludeRegex = v
	}
	nsIncludeSelector = envStr("NAMESPACE_INCLUDE_SELECTOR", nsIncludeSelector)
	nsExcludeSelector = envStr("NAMESPACE_EXCLUDE_SELECTOR", nsExcludeSelector)
	podExcludeSelector = envStr("POD_EXCLUDE_SELECTOR", podExcludeSelector)
	if v, ok := os.LookupEnv("REAP_OWNER_KINDS"); ok {
		ownerKinds = v
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if dryRun {
		setupLog.Info("РЕЖИМ DRY-RUN включён: поды НЕ будут удаляться, только логирование. " +
			"Для реального удаления задайте dry-run=false (Helm: --set config.dryRun=false)")
	}

	filter, err := controller.BuildFilter(
		nsIncludeRegex, nsExcludeRegex,
		nsIncludeSelector, nsExcludeSelector,
		podExcludeSelector,
		ownerKinds,
	)
	if err != nil {
		setupLog.Error(err, "неверные параметры фильтрации")
		os.Exit(1)
	}

	// Ограничение кэша/watch конкретными namespace, если заданы.
	cacheOpts := cache.Options{}
	if namespacesFlag != "" {
		nsMap := map[string]cache.Config{}
		for _, ns := range strings.Split(namespacesFlag, ",") {
			ns = strings.TrimSpace(ns)
			if ns != "" {
				nsMap[ns] = cache.Config{}
			}
		}
		cacheOpts.DefaultNamespaces = nsMap
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Cache:                  cacheOpts,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderEl,
		LeaderElectionID:       "terminating-pod-reaper.nd4y.io",
	})
	if err != nil {
		setupLog.Error(err, "не удалось создать manager")
		os.Exit(1)
	}

	if err = (&controller.PodReaper{
		Client: mgr.GetClient(),
		DryRun: dryRun,
		Filter: filter,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "не удалось запустить контроллер")
		os.Exit(1)
	}

	_ = mgr.AddHealthzCheck("healthz", healthz.Ping)
	_ = mgr.AddReadyzCheck("readyz", healthz.Ping)

	setupLog.Info("старт terminating-pod-reaper",
		"dryRun", dryRun,
		"namespaces", namespacesFlag,
		"nsIncludeRegex", nsIncludeRegex,
		"nsExcludeRegex", nsExcludeRegex,
		"nsIncludeSelector", nsIncludeSelector,
		"nsExcludeSelector", nsExcludeSelector,
		"podExcludeSelector", podExcludeSelector,
		"ownerKinds", ownerKinds)

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "manager остановился с ошибкой")
		os.Exit(1)
	}
}
