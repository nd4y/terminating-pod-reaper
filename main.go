package main

import (
	"flag"
	"os"
	"strconv"
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

		maxDeletions   int
		syncPeriodSecs int
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
	flag.IntVar(&maxDeletions, "max-deletions-per-interval", 50,
		"Максимум подов, удаляемых за один интервал опроса (sync-period). 0 = без ограничения.")
	flag.IntVar(&syncPeriodSecs, "sync-period-seconds", 600,
		"Как часто (сек) полностью ресинкать состояние кластера — страховка от пропущенных watch-событий; "+
			"это же окно лимита удалений. По умолчанию 600 (10 мин).")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Env имеет приоритет над дефолтами флагов (удобно для деплоя через манифест).
	// Для параметров с непустым дефолтом используем LookupEnv: пустое значение env
	// значимо и должно перекрывать дефолт (чтобы Helm был авторитетен).
	if v, ok := os.LookupEnv("DRY_RUN"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			// Непонятное значение предохранителя → остаёмся в безопасном dry-run.
			setupLog.Info("не удалось разобрать DRY_RUN, остаюсь в dry-run", "value", v)
			b = true
		}
		dryRun = b
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
	if v, ok := os.LookupEnv("MAX_DELETIONS_PER_INTERVAL"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			setupLog.Error(err, "некорректный MAX_DELETIONS_PER_INTERVAL", "value", v)
			os.Exit(1)
		}
		maxDeletions = n
	}
	if v, ok := os.LookupEnv("SYNC_PERIOD_SECONDS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			setupLog.Error(err, "некорректный SYNC_PERIOD_SECONDS (должен быть > 0)", "value", v)
			os.Exit(1)
		}
		syncPeriodSecs = n
	}
	if syncPeriodSecs <= 0 {
		setupLog.Error(nil, "sync-period-seconds должен быть > 0", "value", syncPeriodSecs)
		os.Exit(1)
	}
	syncPeriod := time.Duration(syncPeriodSecs) * time.Second

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
	// SyncPeriod — период полного ресинка (опроса) состояния кластера.
	cacheOpts := cache.Options{SyncPeriod: &syncPeriod}
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
		Client:                mgr.GetClient(),
		DryRun:                dryRun,
		Filter:                filter,
		MaxDeletionsPerWindow: maxDeletions,
		Window:                syncPeriod,
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
		"ownerKinds", ownerKinds,
		"maxDeletionsPerInterval", maxDeletions,
		"syncPeriod", syncPeriod.String())

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "manager остановился с ошибкой")
		os.Exit(1)
	}
}
