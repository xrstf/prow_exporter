# Prow Exporter

This is a Prometheus exporter that provides metrics about Prow jobs.

**This is just an experiment and some or all of the metrics might make no sense at all.**

## Usage

Use the Kubernetes manifests in `contrib/kubernetes/` to deploy the exporter.
Deploy it into the same cluster as Prow and into the same namespace please.

## Mode of Operation

The exporter makes the following assumptions:

* Prow jobs are archived after a certain timeframe, so it's impossible and undesirable to
  get a 100% complete picture of all jobs ever.
* Prow jobs exist for at least 2 days after they completed.

The exporter therefore works like this:

Metrics are reset every night at midnight UTC. This means that on Monday, the exporter will report
metrics only for jobs that have be running on or completed during Monday. At midnight in the
night to Tuesday, metrics are reset. Metrics like `prow_job_time_spent_seconds` are a counter,
because Prometheus can internally deal with counters being reset to 0. This is the main reason
for the nightly reset, to have a defined point where we return to 0.

## Metrics

* `prow_exporter_job_cache_size` (gauge) - number of Prow jobs in the in-memory cache of the exporter (no labels)
* `prow_running_jobs` (gauge) - number of running jobs per job name, labels `prowjob`, `type`, `cluster`, `org`, `repo`, `pr`
* `prow_job_time_spent_seconds` (counter) - total time spent on a Prow job, in seconds., labels `prowjob`, `type`, `cluster`, `org`, `repo`, `pr`
* `prow_job_states` (counter) - number of Prow jobs per state, labels `prowjob`, `state`, `type`, `cluster`, `org`, `repo`, `pr`
* `prow_job_durations_seconds` (histogram) - histogram of the Prow jobd urations in seconds, labels `prowjob`, `state`, `type`, `cluster`, `org`, `repo`, `pr`
