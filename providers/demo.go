package providers

import (
	"context"
	"fmt"
	"os"
	"time"

	"clouds/cloud"
)

// Demo providers implement the full UI against built-in sample data: no
// credentials and no network. Downloads write small local files so the whole
// `g` flow can be explored.

type DemoAWS struct{}

func newDemoAWS() *DemoAWS { return &DemoAWS{} }

// ID implements Impl.
func (d *DemoAWS) ID() string { return "aws" }

// Info implements Impl.
func (d *DemoAWS) Info(ctx context.Context) string {
	return "DEMO — sample data, no cloud calls"
}

// Catalog implements Impl.
func (d *DemoAWS) Catalog() cloud.Provider {
	return cloud.Provider{
		ID:   "aws",
		Name: "Amazon Web Services (demo)",
		Services: []cloud.Service{
			{ID: "ec2", Name: "EC2 Instances", Fetch: demoEC2},
			{ID: "s3", Name: "S3 Buckets", Fetch: demoS3Buckets},
			{ID: "lambda", Name: "Lambda Functions", Fetch: demoLambda},
			{ID: "rds", Name: "RDS Instances", Fetch: demoRDS},
			{ID: "logs", Name: "CloudWatch Log Groups", Fetch: demoLogGroups},
		},
	}
}

func agoLabel(days int) string {
	return fmt.Sprintf("%dd", days)
}

func demoEC2(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	type row struct {
		name, id, state, typ, pub, priv, az string
		days                                int
	}
	rows := []row{
		{"web-1", "i-0a1b2c3d4e5f60001", "running", "t3.micro", "54.210.13.42", "10.0.1.23", "us-east-1a", 2},
		{"web-2", "i-0a1b2c3d4e5f60002", "running", "t3.micro", "54.210.13.77", "10.0.1.24", "us-east-1a", 2},
		{"worker-1", "i-0a1b2c3d4e5f60003", "running", "m5.large", "-", "10.0.2.10", "us-east-1b", 9},
		{"db-replica", "i-0a1b2c3d4e5f60004", "stopped", "r5.xlarge", "-", "10.0.3.5", "us-east-1c", 21},
		{"ci-runner", "i-0a1b2c3d4e5f60005", "running", "c6i.2xlarge", "3.89.44.10", "10.0.2.31", "us-east-1b", 0},
		{"bastion", "i-0a1b2c3d4e5f60006", "running", "t3.nano", "54.210.99.5", "10.0.0.9", "us-east-1a", 143},
	}
	var out []cloud.Resource
	for _, r := range rows {
		launched := "1h"
		if r.days > 0 {
			launched = agoLabel(r.days)
		}
		out = append(out, cloud.Resource{
			Kind: "ec2/instance", Name: r.name, ID: r.id, Region: "us-east-1",
			Fields: []cloud.Field{
				{Key: "STATE", Value: r.state}, {Key: "TYPE", Value: r.typ},
				{Key: "PUBLIC IP", Value: r.pub}, {Key: "PRIVATE IP", Value: r.priv},
				{Key: "AZ", Value: r.az}, {Key: "LAUNCHED", Value: launched},
			},
			Console: "https://console.aws.amazon.com/ec2/home",
		})
	}
	return out, nil
}

func demoS3Buckets(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	buckets := []struct {
		name, region string
		days         int
	}{
		{"demo-assets", "us-east-1", 300},
		{"demo-logs-archive", "us-west-2", 640},
		{"demo-backups", "eu-west-1", 31},
	}
	var out []cloud.Resource
	for _, b := range buckets {
		name := b.name
		out = append(out, cloud.Resource{
			Kind: "s3/bucket", Name: name, ID: "s3://" + name, Region: b.region,
			Fields: []cloud.Field{
				{Key: "REGION", Value: b.region},
				{Key: "CREATED", Value: agoLabel(b.days)},
			},
			Console: "https://s3.console.aws.amazon.com/s3/buckets",
			Sub: func(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
				return demoS3Objects(name)
			},
		})
	}
	return out, nil
}

func demoS3Objects(bucket string) ([]cloud.Resource, error) {
	out := []cloud.Resource{{
		Kind: "s3/object", Name: "backups/", ID: "0 B",
		Fields: []cloud.Field{{Key: "STORAGE", Value: "-"}, {Key: "MODIFIED", Value: "5d"}},
		Detail: fmt.Sprintf("bucket:   %s\nfolder marker — `g` downloads the whole prefix recursively", bucket),
		Download: &cloud.Download{
			Label: "s3 folder (demo)",
			Run: func(ctx context.Context, opts cloud.Options, progress func(string)) (string, error) {
				target := cloud.SafePath(opts.DownloadDir, bucket, "backups/db-2026-09-15.sql")
				if err := os.MkdirAll(dirOf(target), 0o755); err != nil {
					return "", err
				}
				progress("demo payload  backups/db-2026-09-15.sql")
				time.Sleep(300 * time.Millisecond)
				if err := os.WriteFile(target, []byte("demo content for "+bucket+"/backups/db-2026-09-15.sql\n"), 0o644); err != nil {
					return "", err
				}
				return target, nil
			},
		},
	}}
	type obj struct {
		key, size string
		days      int
	}
	for _, o := range []obj{
		{"data/export-2026-09.csv", "1.8 MiB", 0},
		{"data/export-2026-08.csv", "1.7 MiB", 30},
		{"images/logo.png", "84 KiB", 143},
		{"logs/app-2026-09-16.log", "12.4 MiB", 0},
		{"readme.txt", "2 KiB", 365},
	} {
		o := o
		days := "1h"
		if o.days > 0 {
			days = agoLabel(o.days)
		}
		out = append(out, cloud.Resource{
			Kind: "s3/object", Name: o.key, ID: o.size,
			Fields:  []cloud.Field{{Key: "STORAGE", Value: "STANDARD"}, {Key: "MODIFIED", Value: days}},
			Detail:  fmt.Sprintf("bucket:   %s\nkey:      %s\nsize:     %s", bucket, o.key, o.size),
			Console: fmt.Sprintf("https://s3.console.aws.amazon.com/s3/buckets/%s?prefix=%s", bucket, o.key),
			Download: &cloud.Download{
				Label: "demo object",
				Run: func(ctx context.Context, opts cloud.Options, progress func(string)) (string, error) {
					return demoDownload(opts, bucket, o.key, progress)
				},
			},
		})
	}
	return out, nil
}

func demoDownload(opts cloud.Options, bucket, key string, progress func(string)) (string, error) {
	target := cloud.SafePath(opts.DownloadDir, bucket, key)
	if err := os.MkdirAll(dirOf(target), 0o755); err != nil {
		return "", err
	}
	progress(fmt.Sprintf("demo payload  %s", key))
	time.Sleep(300 * time.Millisecond)
	if err := os.WriteFile(target, []byte(fmt.Sprintf("demo content for %s/%s\n", bucket, key)), 0o644); err != nil {
		return "", err
	}
	return target, nil
}

func demoLambda(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	rows := []struct {
		name, runtime, mem, timeout string
		days                        int
	}{
		{"api-gateway-proxy", "python3.12", "512 MB", "30s", 0},
		{"image-resizer", "python3.12", "1024 MB", "60s", 12},
		{"nightly-rollup", "nodejs20.x", "256 MB", "900s", 3},
	}
	var out []cloud.Resource
	for _, r := range rows {
		modified := "1h"
		if r.days > 0 {
			modified = agoLabel(r.days)
		}
		out = append(out, cloud.Resource{
			Kind: "lambda/function", Name: r.name, ID: r.name, Region: "us-east-1",
			Fields: []cloud.Field{
				{Key: "RUNTIME", Value: r.runtime}, {Key: "MEMORY", Value: r.mem},
				{Key: "TIMEOUT", Value: r.timeout}, {Key: "MODIFIED", Value: modified},
			},
			Console: "https://console.aws.amazon.com/lambda/home",
		})
	}
	return out, nil
}

func demoRDS(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	rows := []struct {
		name, engine, class, status, endpoint string
		multiAZ                               bool
	}{
		{"prod-pg", "postgres 16.3", "db.r6g.xlarge", "available", "prod-pg.abc123.us-east-1.rds.amazonaws.com", true},
		{"staging-mysql", "mysql 8.0.36", "db.t3.medium", "available", "staging.abc123.us-east-1.rds.amazonaws.com", false},
	}
	var out []cloud.Resource
	for _, r := range rows {
		multiAZ := "no"
		if r.multiAZ {
			multiAZ = "yes"
		}
		out = append(out, cloud.Resource{
			Kind: "rds/instance", Name: r.name, ID: r.name, Region: "us-east-1",
			Fields: []cloud.Field{
				{Key: "STATUS", Value: r.status}, {Key: "ENGINE", Value: r.engine},
				{Key: "CLASS", Value: r.class}, {Key: "ENDPOINT", Value: r.endpoint},
				{Key: "MULTI-AZ", Value: multiAZ},
			},
			Console: "https://console.aws.amazon.com/rds/home",
		})
	}
	return out, nil
}

func demoLogGroups(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	groups := []struct {
		name, stored string
		retention    int
	}{
		{"/aws/lambda/api-gateway-proxy", "1.2 GiB", 30},
		{"/ecs/worker/prod", "860 MiB", 14},
	}
	var out []cloud.Resource
	for _, g := range groups {
		name := g.name
		out = append(out, cloud.Resource{
			Kind: "logs/group", Name: name, ID: name, Region: "us-east-1",
			Fields: []cloud.Field{
				{Key: "STORED", Value: g.stored},
				{Key: "RETENTION", Value: fmt.Sprintf("%dd", g.retention)},
			},
			Console: "https://console.aws.amazon.com/cloudwatch/home",
			Sub: func(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
				events := []struct {
					time, msg, stream string
				}{
					{"2026-09-16 09:14:03", "GET /healthz 200 3ms", "i-04f2a1"},
					{"2026-09-16 09:13:58", "GET /v1/users 200 42ms", "i-04f2a1"},
					{"2026-09-16 09:12:41", "WARN slow query 1.8s table=orders", "i-04f2a2"},
					{"2026-09-16 09:11:09", "POST /v1/orders 201 118ms", "i-04f2a1"},
				}
				var rows []cloud.Resource
				for i, e := range events {
					rows = append(rows, cloud.Resource{
						Kind: "logs/event", Name: cloud.Trunc(e.msg, 120), ID: fmt.Sprintf("ev-%d", i), Region: "us-east-1",
						Fields: []cloud.Field{{Key: "TIME", Value: e.time}, {Key: "STREAM", Value: e.stream}},
						Detail: fmt.Sprintf("stream:   %s\ntime:     %s\n\n%s", e.stream, e.time, e.msg),
					})
				}
				return rows, nil
			},
		})
	}
	return out, nil
}

// ------------------------------------------------------------------- GCP ---

type DemoGCP struct{}

func newDemoGCP() *DemoGCP { return &DemoGCP{} }

// ID implements Impl.
func (d *DemoGCP) ID() string { return "gcp" }

// Info implements Impl.
func (d *DemoGCP) Info(ctx context.Context) string {
	return "DEMO — sample data, no cloud calls"
}

// Catalog implements Impl.
func (d *DemoGCP) Catalog() cloud.Provider {
	return cloud.Provider{
		ID:   "gcp",
		Name: "Google Cloud Platform (demo)",
		Services: []cloud.Service{
			{ID: "gce", Name: "Compute Engine VMs", Alias: "compute", Fetch: demoGCE},
			{ID: "gcs", Name: "GCS Buckets", Fetch: demoGCS},
			{ID: "gke", Name: "GKE Clusters", Fetch: demoGKE},
			{ID: "run", Name: "Cloud Run Services", Fetch: demoRun},
			{ID: "glogs", Name: "Cloud Logging (last 6h)", Fetch: demoGLogs},
		},
	}
}

func demoGCE(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	rows := []struct {
		name, zone, status, typ, ext, inten string
	}{
		{"web-frontend-1", "us-central1-a", "RUNNING", "e2-medium", "34.170.12.5", "10.10.0.2"},
		{"web-frontend-2", "us-central1-b", "RUNNING", "e2-medium", "34.170.12.6", "10.10.1.2"},
		{"batch-worker", "us-central1-a", "TERMINATED", "n2-standard-4", "-", "10.10.2.7"},
		{"db-primary", "us-east1-c", "RUNNING", "db-custom-4-16384", "-", "10.20.0.3"},
	}
	var out []cloud.Resource
	for i, r := range rows {
		out = append(out, cloud.Resource{
			Kind: "gce/instance", Name: r.name, ID: fmt.Sprintf("%d", 1000+i), Region: r.zone,
			Fields: []cloud.Field{
				{Key: "ZONE", Value: r.zone}, {Key: "STATUS", Value: r.status},
				{Key: "TYPE", Value: r.typ}, {Key: "EXTERNAL IP", Value: r.ext},
				{Key: "INTERNAL IP", Value: r.inten},
			},
			Console: "https://console.cloud.google.com/compute/instances",
		})
	}
	return out, nil
}

func demoGCS(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	return []cloud.Resource{{
		Kind: "gcs/bucket", Name: "demo-assets-gcs", ID: "gs://demo-assets-gcs", Region: "US",
		Fields: []cloud.Field{
			{Key: "LOCATION", Value: "US"}, {Key: "CLASS", Value: "STANDARD"}, {Key: "CREATED", Value: "310d"},
		},
		Console: "https://console.cloud.google.com/storage/browser",
		Sub: func(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
			var out []cloud.Resource
			for _, o := range []struct {
				key, size string
				days      int
			}{
				{"artifacts/app-1.4.2.tar.gz", "220 MiB", 0},
				{"artifacts/app-1.4.1.tar.gz", "219 MiB", 14},
				{"data/users.parquet", "640 MiB", 2},
				{"static/hero.webp", "312 KiB", 60},
			} {
				modified := "1h"
				if o.days > 0 {
					modified = agoLabel(o.days)
				}
				key := o.key
				out = append(out, cloud.Resource{
					Kind: "gcs/object", Name: key, ID: o.size,
					Fields: []cloud.Field{{Key: "STORAGE", Value: "STANDARD"}, {Key: "MODIFIED", Value: modified}},
					Detail: fmt.Sprintf("bucket:   demo-assets-gcs\nkey:      %s\nsize:     %s", key, o.size),
					Download: &cloud.Download{
						Label: "demo object",
						Run: func(ctx context.Context, opts cloud.Options, progress func(string)) (string, error) {
							return demoDownload(opts, "demo-assets-gcs", key, progress)
						},
					},
				})
			}
			return out, nil
		},
	}}, nil
}

func demoGKE(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	return []cloud.Resource{{
		Kind: "gke/cluster", Name: "prod-east", ID: "prod-east", Region: "us-east1",
		Fields: []cloud.Field{
			{Key: "LOCATION", Value: "us-east1"}, {Key: "VERSION", Value: "1.30.5-gke.1"},
			{Key: "STATUS", Value: "RUNNING"}, {Key: "NODES", Value: "12"},
		},
		Console: "https://console.cloud.google.com/kubernetes/clusters",
	}}, nil
}

func demoRun(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	rows := []struct {
		name, region, url, ready string
	}{
		{"checkout-api", "us-central1", "https://checkout-api-abc-uw.a.run.app", "True"},
		{"image-proxy", "us-central1", "https://image-proxy-abc-el.a.run.app", "True"},
	}
	var out []cloud.Resource
	for i, r := range rows {
		out = append(out, cloud.Resource{
			Kind: "run/service", Name: r.name, ID: fmt.Sprintf("uid-%d", i), Region: r.region,
			Fields: []cloud.Field{
				{Key: "REGION", Value: r.region}, {Key: "READY", Value: r.ready}, {Key: "URL", Value: cloud.Trunc(r.url, 48)},
			},
			Console: "https://console.cloud.google.com/run",
		})
	}
	return out, nil
}

func demoGLogs(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	rows := []struct {
		time, severity, log, msg string
	}{
		{"2026-09-16 09:14:03", "INFO", "checkout-api", "request completed status=200 latency=42ms"},
		{"2026-09-16 09:13:51", "WARNING", "image-proxy", "upstream slow response 1.4s retrying"},
		{"2026-09-16 09:12:30", "ERROR", "checkout-api", "payment provider timeout after 30s txn=8842"},
		{"2026-09-16 09:10:02", "INFO", "prod-east", "autoscaling: nodes 12 -> 14"},
	}
	var out []cloud.Resource
	for i, r := range rows {
		out = append(out, cloud.Resource{
			Kind: "log/entry", Name: cloud.Trunc(r.msg, 120), ID: fmt.Sprintf("ins-%d", i),
			Fields: []cloud.Field{
				{Key: "TIME", Value: r.time}, {Key: "SEVERITY", Value: r.severity}, {Key: "LOG", Value: r.log},
			},
			Detail: fmt.Sprintf("log:      %s\ntime:     %s\nseverity: %s\n\n%s", r.log, r.time, r.severity, r.msg),
		})
	}
	return out, nil
}
