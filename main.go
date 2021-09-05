package main

import (
	"context"
	"flag"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowjobset "k8s.io/test-infra/prow/client/clientset/versioned"
	prowjobinfo "k8s.io/test-infra/prow/client/informers/externalversions"
)

type options struct {
	kubeconfig string
	listenAddr string
	namespace  string
	debugLog   bool
}

func calculateCutoff() time.Time {
	return time.Now().UTC().Truncate(24 * time.Hour)
}

var (
	jobCache = map[string]*prowjobv1.ProwJob{}
	lock     = &sync.RWMutex{}
)

func main() {
	opt := options{
		listenAddr: ":9855",
	}

	flag.StringVar(&opt.kubeconfig, "kubeconfig", "", "Kubeconfig file for the Prow controlplane cluster (only required if out-of-cluster)")
	flag.StringVar(&opt.listenAddr, "listen", opt.listenAddr, "address and port to listen on")
	flag.StringVar(&opt.namespace, "namespace", opt.namespace, "namespace to watch Prowjobs in")
	flag.BoolVar(&opt.debugLog, "debug", opt.debugLog, "enable more verbose logging")
	flag.Parse()

	// setup logging
	var log = logrus.New()
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: time.RFC1123,
	})

	if opt.debugLog {
		log.SetLevel(logrus.DebugLevel)
	}

	if opt.namespace == "" {
		log.Fatal("A -namespace must be given.")
	}

	// setup kubernetes client
	config, err := clientcmd.BuildConfigFromFlags("", opt.kubeconfig)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// clientset, err := kubernetes.NewForConfig(config)
	// if err != nil {
	// 	log.Fatalf("Failed to create Kubernetes clientset: %v", err)
	// }

	pjc, err := prowjobset.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create ProwJob clientset: %v", err)
	}

	factory := prowjobinfo.NewSharedInformerFactoryWithOptions(pjc, 0, prowjobinfo.WithNamespace(opt.namespace))
	informer := factory.Prow().V1().ProwJobs().Informer()
	eventHandler := createNewEventHandler(log)

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			eventHandler(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			eventHandler(newObj)
		},
	})

	ctx := context.Background()

	go func() {
		log.Println("Starting informer...")
		informer.Run(ctx.Done())
	}()

	for !informer.HasSynced() {
		time.Sleep(1 * time.Second)
	}

	log.Info("Preparing metrics collector…")
	prometheus.MustRegister(NewCollector(log))

	http.Handle("/metrics", promhttp.Handler())
	log.Printf("Starting server on %s…", opt.listenAddr)
	log.Fatal(http.ListenAndServe(opt.listenAddr, nil))
}

func createNewEventHandler(log logrus.FieldLogger) func(a interface{}) {
	return func(a interface{}) {
		prowJob, ok := a.(*prowjobv1.ProwJob)
		if !ok {
			log.Warn("Not a ProwJob")
			return
		}

		l := log.WithField("job", prowJob.GetName())

		lock.Lock()
		defer lock.Unlock()

		grave := calculateCutoff().Add(-24 * time.Hour)

		// garbage-collect ancient jobs to keep the jobCache minimal
		garbageCollectJobs(grave)

		// ignore jobs entirely if they completed a long time ago
		if !prowJob.Status.CompletionTime.IsZero() && prowJob.Status.CompletionTime.Time.Before(grave) {
			return
		}

		jobCache[prowJob.GetName()] = prowJob
		l.Debug("Received notification.")
	}
}

func garbageCollectJobs(grave time.Time) {
	for name, job := range jobCache {
		if !job.Status.CompletionTime.IsZero() && job.Status.CompletionTime.Time.Before(grave) {
			delete(jobCache, name)
		}
	}
}

type Collector struct {
	log *logrus.Logger
}

func NewCollector(log *logrus.Logger) *Collector {
	return &Collector{
		log: log,
	}
}

func (mc *Collector) Describe(ch chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(mc, ch)
}

func (mc *Collector) Collect(ch chan<- prometheus.Metric) {
	mc.log.Debug("Collecting metrics…")

	if err := mc.collect(ch); err != nil {
		mc.log.Errorf("Failed to collect metrics: %v", err)
	}

	mc.log.Debug("Done collecting metrics.")
}

func (mc *Collector) collect(ch chan<- prometheus.Metric) error {
	lock.RLock()
	defer lock.RUnlock()

	cutoff := calculateCutoff()
	now := time.Now().UTC().Truncate(1 * time.Second)

	// metric: number of jobs in the jobCache map
	cacheSize := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "prow_exporter",
		Name:      "job_cache_size",
		Help:      "Number of Prow jobs in the in-memory cache of the exporter.",
	})

	cacheSize.Set(float64(len(jobCache)))
	cacheSize.Collect(ch)

	// metric: number of running jobs per job name
	// NB: "running" in terms of Prow means "is scheduled to a cluster",
	// where the pod can be pending because the autoscaler needs to kick in first
	runningJobs := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "prow",
		Name:      "running_jobs",
		Help:      "Number of running jobs per job name.",
	}, []string{"prowjob", "type", "cluster", "org", "repo", "pr"})

	for _, job := range jobCache {
		if !isJobRunning(job) {
			continue
		}

		runningJobs.WithLabelValues(
			job.Spec.Job,
			string(job.Spec.Type),
			job.Spec.Cluster,
			job.Labels["prow.k8s.io/refs.org"],
			job.Labels["prow.k8s.io/refs.repo"],
			job.Labels["prow.k8s.io/refs.pull"],
		).Inc()
	}

	runningJobs.Collect(ch)

	// metric: time spent on each job type
	timeSpent := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "prow",
		Name:      "job_time_spent_seconds",
		Help:      "Total time spent on a Prow job, in seconds.",
	}, []string{"prowjob", "type", "cluster", "org", "repo", "pr"})

	// What we output here is a *counter* that gets reset on every
	// cutoff (midnight). So when a job started at 23:00 and we're
	// scraped at 00:25, we report 25 minutes spent on this job.
	// This includes jobs that are not running anymore, but have
	// finished at some point during the day.

	for _, job := range jobCache {
		fitsInCurrentWindow, start, end := clampToCurrentDay(job, now, cutoff)
		if !fitsInCurrentWindow {
			continue
		}

		timeSpent.WithLabelValues(
			job.Spec.Job,
			string(job.Spec.Type),
			job.Spec.Cluster,
			job.Labels["prow.k8s.io/refs.org"],
			job.Labels["prow.k8s.io/refs.repo"],
			job.Labels["prow.k8s.io/refs.pull"],
		).Add(end.Sub(start).Seconds())
	}

	timeSpent.Collect(ch)

	// metric: number of successful/failed jobs
	jobStatus := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "prow",
		Name:      "job_states",
		Help:      "Number of Prow jobs per state.",
	}, []string{"prowjob", "state", "type", "cluster", "org", "repo", "pr"})

	seenJobNames := sets.NewString()
	allStates := []prowjobv1.ProwJobState{
		prowjobv1.TriggeredState,
		prowjobv1.PendingState,
		prowjobv1.SuccessState,
		prowjobv1.FailureState,
		prowjobv1.AbortedState,
		prowjobv1.ErrorState,
	}

	for _, job := range jobCache {
		fitsInCurrentWindow, _, _ := clampToCurrentDay(job, now, cutoff)
		if !fitsInCurrentWindow {
			continue
		}

		// missing label combaintions for all states can make
		// queries harder (e.g. if a job has _only_ failures and not
		// a single success), so we zero the metric for all known states
		if !seenJobNames.Has(job.Spec.Job) {
			for _, state := range allStates {
				jobStatus.WithLabelValues(
					job.Spec.Job,
					string(state),
					string(job.Spec.Type),
					job.Spec.Cluster,
					job.Labels["prow.k8s.io/refs.org"],
					job.Labels["prow.k8s.io/refs.repo"],
					job.Labels["prow.k8s.io/refs.pull"],
				)
			}

			seenJobNames.Insert(job.Spec.Job)
		}

		jobStatus.WithLabelValues(
			job.Spec.Job,
			string(job.Status.State),
			string(job.Spec.Type),
			job.Spec.Cluster,
			job.Labels["prow.k8s.io/refs.org"],
			job.Labels["prow.k8s.io/refs.repo"],
			job.Labels["prow.k8s.io/refs.pull"],
		).Inc()
	}

	jobStatus.Collect(ch)

	// metric: job durations as a histogram
	jobDurations := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "prow",
		Name:      "job_durations_seconds",
		Help:      "Histogram of the Prow jobd urations in seconds.",
		Buckets: []float64{
			30,
			60,
			5 * 60,
			10 * 60,
			15 * 60,
			30 * 60,
			45 * 60,
			60 * 60,
			75 * 60,
			90 * 60,
			105 * 60,
			120 * 60,
		},
	}, []string{"prowjob", "state", "type", "cluster", "org", "repo", "pr"})

	seenJobNames = sets.NewString()

	for _, job := range jobCache {
		if job.Status.PendingTime.IsZero() || job.Status.CompletionTime.IsZero() || job.Status.CompletionTime.Time.Before(cutoff) {
			continue
		}

		if !seenJobNames.Has(job.Spec.Job) {
			for _, state := range allStates {
				jobDurations.WithLabelValues(
					job.Spec.Job,
					string(state),
					string(job.Spec.Type),
					job.Spec.Cluster,
					job.Labels["prow.k8s.io/refs.org"],
					job.Labels["prow.k8s.io/refs.repo"],
					job.Labels["prow.k8s.io/refs.pull"],
				)
			}

			seenJobNames.Insert(job.Spec.Job)
		}

		duration := job.Status.CompletionTime.Time.Sub(job.Status.PendingTime.Time)

		jobDurations.WithLabelValues(
			job.Spec.Job,
			string(job.Status.State),
			string(job.Spec.Type),
			job.Spec.Cluster,
			job.Labels["prow.k8s.io/refs.org"],
			job.Labels["prow.k8s.io/refs.repo"],
			job.Labels["prow.k8s.io/refs.pull"],
		).Observe(duration.Seconds())
	}

	jobDurations.Collect(ch)

	return nil
}

func isJobRunning(pj *prowjobv1.ProwJob) bool {
	return !pj.Status.PendingTime.IsZero() && pj.Status.CompletionTime.IsZero()
}

func clampToCurrentDay(pj *prowjobv1.ProwJob, now time.Time, cutoff time.Time) (bool, time.Time, time.Time) {
	// not yet triggered
	if pj.Status.PendingTime.IsZero() {
		return false, time.Time{}, time.Time{}
	}

	var completion *time.Time
	if pj.Status.CompletionTime != nil {
		completion = &pj.Status.CompletionTime.Time
	}

	// completed already before the cutoff
	if completion != nil && !completion.IsZero() && completion.Before(cutoff) {
		return false, time.Time{}, time.Time{}
	}

	// start is the start of the job, clamped to the cutoff time
	start := pj.Status.PendingTime.Time
	if start.Before(cutoff) {
		start = cutoff
	}

	end := now
	if completion != nil {
		end = *completion
	}

	return true, start, end
}
