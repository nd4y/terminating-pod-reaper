package main

import (
	"flag"
	"os"
	"strings"
	"time"

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

// envDuration читает длительность в секундах из переменной окружения.
func envDurationSeconds(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v + "s"); err == nil {
			return d
		}
	}
	return def
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
		thresholdSecs  int
		dryRun         bool
		namespacesFlag string

		nsIncludeRegex    string
		nsExcludeRegex    string
		nsIncludeSelector string
		nsExcludeSelector string
		podExcludeSelector string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Адрес для /metrics.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Адрес для health/ready проб.")
	flag.BoolVar(&enableLeaderEl, "leader-elect", false, "Включить leader election для HA (2+ реплик).")
	flag.IntVar(&thresholdSecs, "threshold-seconds", 120,
		"Через сколько секунд после перехода в Terminating под удаляется. По умолчанию 120.")
	flag.BoolVar(&dryRun, "dry-run", false, "Только логировать, ничего не удалять.")
	flag.StringVar(&namespacesFlag, "namespaces", "",
		"Жёсткое ограничение watch списком namespace через запятую. Пусто = весь кластер.")
	flag.StringVar(&nsIncludeRegex, "namespace-include-regex", "",
		"Обрабатывать только namespace, чьё имя совпадает с regex.")
	flag.StringVar(&nsExcludeRegex, "namespace-exclude-regex", "",
		"Исключить namespace, чьё имя совпадает с regex.")
	flag.StringVar(&nsIncludeSelector, "namespace-include-selector", "",
		"Обрабатывать только namespace с метками, совпадающими с label selector (напр. 'reaper=enabled').")
	flag.StringVar(&nsExcludeSelector, "namespace-exclude-selector", "",
		"Исключить namespace с метками, совпадающими с label selector.")
	flag.StringVar(&podExcludeSelector, "pod-exclude-selector", "",
		"Не трогать поды с метками, совпадающими с label selector (напр. 'reaper.io/ignore=true').")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	// Env имеет приоритет над дефолтами флагов (удобно для деплоя через манифест).
	threshold := envDurationSeconds("THRESHOLD_SECONDS", time.Duration(thresholdSecs)*time.Second)
	if v := os.Getenv("DRY_RUN"); v == "true" {
		dryRun = true
	}
	namespacesFlag = envStr("NAMESPACES", namespacesFlag)
	nsIncludeRegex = envStr("NAMESPACE_INCLUDE_REGEX", nsIncludeRegex)
	nsExcludeRegex = envStr("NAMESPACE_EXCLUDE_REGEX", nsExcludeRegex)
	nsIncludeSelector = envStr("NAMESPACE_INCLUDE_SELECTOR", nsIncludeSelector)
	nsExcludeSelector = envStr("NAMESPACE_EXCLUDE_SELECTOR", nsExcludeSelector)
	podExcludeSelector = envStr("POD_EXCLUDE_SELECTOR", podExcludeSelector)

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	filter, err := controller.BuildFilter(
		nsIncludeRegex, nsExcludeRegex,
		nsIncludeSelector, nsExcludeSelector,
		podExcludeSelector,
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
		Client:    mgr.GetClient(),
		Threshold: threshold,
		DryRun:    dryRun,
		Filter:    filter,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "не удалось запустить контроллер")
		os.Exit(1)
	}

	_ = mgr.AddHealthzCheck("healthz", healthz.Ping)
	_ = mgr.AddReadyzCheck("readyz", healthz.Ping)

	setupLog.Info("старт reaper'а",
		"threshold", threshold.String(),
		"dryRun", dryRun,
		"namespaces", namespacesFlag,
		"nsIncludeRegex", nsIncludeRegex,
		"nsExcludeRegex", nsExcludeRegex,
		"nsIncludeSelector", nsIncludeSelector,
		"nsExcludeSelector", nsExcludeSelector,
		"podExcludeSelector", podExcludeSelector)

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "manager остановился с ошибкой")
		os.Exit(1)
	}
}
