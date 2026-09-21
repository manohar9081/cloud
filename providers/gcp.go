package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/compute/metadata"
	"google.golang.org/api/bigquery/v2"
	"google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/container/v1"
	"google.golang.org/api/logging/v2"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
	runapi "google.golang.org/api/run/v1"
	"google.golang.org/api/sqladmin/v1"
	"google.golang.org/api/storage/v1"

	"clouds/cloud"
)

// GCP is the Google Cloud backend.
type GCP struct {
	opts    cloud.Options
	mu      sync.Mutex
	project string
	svcs    map[string]any
}

// NewGCP returns the GCP backend (credentials via Application Default
// Credentials; project from --project, env or the metadata server).
func NewGCP(opts cloud.Options) *GCP {
	return &GCP{opts: opts, svcs: map[string]any{}}
}

// ID implements Impl.
func (g *GCP) ID() string { return "gcp" }

// Info implements Impl.
func (g *GCP) Info(ctx context.Context) string {
	return fmt.Sprintf("project=%s", orDefault(g.Project(ctx), "(unset)"))
}

// Catalog implements Impl.
func (g *GCP) Catalog() cloud.Provider {
	return cloud.Provider{
		ID:   "gcp",
		Name: "Google Cloud Platform",
		Services: []cloud.Service{
			{ID: "gce", Name: "Compute Engine VMs", Alias: "compute", Fetch: g.fetchGCE},
			{ID: "gcs", Name: "GCS Buckets", Fetch: g.fetchGCS},
			{ID: "gke", Name: "GKE Clusters", Fetch: g.fetchGKE},
			{ID: "run", Name: "Cloud Run Services", Fetch: g.fetchRun},
			{ID: "fn", Name: "Cloud Functions", Alias: "functions", Fetch: g.fetchFunctions},
			{ID: "bq", Name: "BigQuery Datasets", Fetch: g.fetchBQ},
			{ID: "pubsub", Name: "Pub/Sub Topics", Fetch: g.fetchPubSub},
			{ID: "sql", Name: "Cloud SQL Instances", Fetch: g.fetchSQL},
			{ID: "glogs", Name: "Cloud Logging (last 6h)", Alias: "log", Fetch: g.fetchGLogs},
		},
	}
}

// Project resolves the GCP project id (flag -> env -> metadata server).
func (g *GCP) Project(ctx context.Context) string {
	g.mu.Lock()
	if g.project != "" {
		p := g.project
		g.mu.Unlock()
		return p
	}
	g.mu.Unlock()
	for _, key := range []string{"GOOGLE_CLOUD_PROJECT", "GCP_PROJECT", "GCLOUD_PROJECT"} {
		if v := os.Getenv(key); v != "" {
			g.mu.Lock()
			g.project = v
			g.mu.Unlock()
			return v
		}
	}
	ch := make(chan string, 1)
	go func() {
		if p, err := metadata.ProjectID(); err == nil {
			ch <- p
		} else {
			ch <- ""
		}
	}()
	select {
	case p := <-ch:
		if p != "" {
			g.mu.Lock()
			g.project = p
			g.mu.Unlock()
		}
		return p
	case <-ctx.Done():
		return ""
	case <-time.After(3 * time.Second):
		return ""
	}
}

func (g *GCP) requireProject(ctx context.Context) (string, error) {
	if p := g.Project(ctx); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("no GCP project: pass --project, set GOOGLE_CLOUD_PROJECT, or run on GCP")
}

// svc lazily builds and caches a Discovery API client.
func svc[T any](g *GCP, key string, build func(context.Context, ...option.ClientOption) (T, error)) (T, error) {
	var zero T
	g.mu.Lock()
	if s, ok := g.svcs[key]; ok {
		g.mu.Unlock()
		return s.(T), nil
	}
	g.mu.Unlock()
	s, err := build(context.Background())
	if err != nil {
		return zero, err
	}
	g.mu.Lock()
	g.svcs[key] = s
	g.mu.Unlock()
	return s, nil
}

func (g *GCP) computeSvc(ctx context.Context) (*compute.Service, error) {
	return svc(g, "compute/v1", compute.NewService)
}
func (g *GCP) storageSvc(ctx context.Context) (*storage.Service, error) {
	return svc(g, "storage/v1", storage.NewService)
}
func (g *GCP) containerSvc(ctx context.Context) (*container.Service, error) {
	return svc(g, "container/v1", container.NewService)
}
func (g *GCP) runSvc(ctx context.Context) (*runapi.APIService, error) {
	return svc(g, "run/v1", runapi.NewService)
}
func (g *GCP) functionsSvc(ctx context.Context) (*cloudfunctions.Service, error) {
	return svc(g, "cloudfunctions/v2", cloudfunctions.NewService)
}
func (g *GCP) bqSvc(ctx context.Context) (*bigquery.Service, error) {
	return svc(g, "bigquery/v2", bigquery.NewService)
}
func (g *GCP) pubsubSvc(ctx context.Context) (*pubsub.Service, error) {
	return svc(g, "pubsub/v1", pubsub.NewService)
}
func (g *GCP) sqlSvc(ctx context.Context) (*sqladmin.Service, error) {
	return svc(g, "sqladmin/v1", sqladmin.NewService)
}
func (g *GCP) loggingSvc(ctx context.Context) (*logging.Service, error) {
	return svc(g, "logging/v2", logging.NewService)
}

func lastSegment(s string) string { return cloud.Short(s) }

func (g *GCP) fetchGCE(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	p, err := g.requireProject(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := g.computeSvc(ctx)
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	req := svc.Instances.AggregatedList(p)
	if err := req.Pages(ctx, func(page *compute.InstanceAggregatedList) error {
		for _, scoped := range page.Items {
			for _, i := range scoped.Instances {
				if len(out) >= cloud.MaxRows {
					return nil
				}
				zone := lastSegment(i.Zone)
				ext, inten := "-", "-"
				if len(i.NetworkInterfaces) > 0 {
					inten = orDefault(i.NetworkInterfaces[0].NetworkIP, "-")
					for _, ac := range i.NetworkInterfaces[0].AccessConfigs {
						if ac.NatIP != "" {
							ext = ac.NatIP
							break
						}
					}
				}
				out = append(out, cloud.Resource{
					Kind: "gce/instance", Name: i.Name, ID: fmt.Sprintf("%d", i.Id), Region: zone,
					Fields: []cloud.Field{
						{Key: "ZONE", Value: zone},
						{Key: "STATUS", Value: i.Status},
						{Key: "TYPE", Value: lastSegment(i.MachineType)},
						{Key: "EXTERNAL IP", Value: ext},
						{Key: "INTERNAL IP", Value: inten},
					},
					Console: fmt.Sprintf("https://console.cloud.google.com/compute/instancesDetail/zones/%s/instances/%s?project=%s", zone, i.Name, p),
				})
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GCP) fetchGCS(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	p, err := g.requireProject(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := g.storageSvc(ctx)
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	if err := svc.Buckets.List(p).Pages(ctx, func(page *storage.Buckets) error {
		for _, b := range page.Items {
			created := "-"
			if t, err := time.Parse(time.RFC3339, b.TimeCreated); err == nil {
				created = cloud.Ago(t)
			}
			name := b.Name
			out = append(out, cloud.Resource{
				Kind: "gcs/bucket", Name: name, ID: "gs://" + name, Region: b.Location,
				Fields: []cloud.Field{
					{Key: "LOCATION", Value: b.Location},
					{Key: "CLASS", Value: b.StorageClass},
					{Key: "CREATED", Value: created},
				},
				Console: fmt.Sprintf("https://console.cloud.google.com/storage/browser/%s?project=%s", name, p),
				Sub: func(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
					return g.gcsObjects(ctx, name)
				},
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GCP) gcsObjects(ctx context.Context, bucket string) ([]cloud.Resource, error) {
	svc, err := g.storageSvc(ctx)
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	if err := svc.Objects.List(bucket).Pages(ctx, func(page *storage.Objects) error {
		for _, o := range page.Items {
			if len(out) >= 300 {
				return nil
			}
			updated := "-"
			if t, err := time.Parse(time.RFC3339, o.Updated); err == nil {
				updated = cloud.Ago(t)
			}
			size := cloud.HumanSize(int64(o.Size))
			key := o.Name
			out = append(out, cloud.Resource{
				Kind: "gcs/object", Name: key, ID: size,
				Fields: []cloud.Field{
					{Key: "STORAGE", Value: o.StorageClass},
					{Key: "MODIFIED", Value: updated},
				},
				Detail: fmt.Sprintf("bucket:   %s\nkey:      %s\nsize:     %s\nmodified: %s", bucket, key, size, updated),
				Download: &cloud.Download{
					Label: "gcs object",
					Run: func(ctx context.Context, opts cloud.Options, progress func(string)) (string, error) {
						return g.gcsDownload(ctx, opts, bucket, key, progress)
					},
				},
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GCP) gcsDownload(ctx context.Context, opts cloud.Options, bucket, key string, progress func(string)) (string, error) {
	svc, err := g.storageSvc(ctx)
	if err != nil {
		return "", err
	}
	target := cloud.SafePath(opts.DownloadDir, bucket, key)
	if err := os.MkdirAll(dirOf(target), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(target)
	if err != nil {
		return "", err
	}
	defer f.Close()
	cw := &progressWriter{key: key, progress: progress}
	// Download returns the raw HTTP response; the body carries the object.
	resp, err := svc.Objects.Get(bucket, key).Context(ctx).Download()
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(cw, resp.Body); err != nil {
		return "", err
	}
	return target, nil
}

func dirOf(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[:i]
	}
	return "."
}

func (g *GCP) fetchGKE(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	p, err := g.requireProject(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := g.containerSvc(ctx)
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	parent := fmt.Sprintf("projects/%s/locations/-", p)
	resp, err := svc.Projects.Locations.Clusters.List(parent).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	for _, c := range resp.Clusters {
		out = append(out, cloud.Resource{
			Kind: "gke/cluster", Name: c.Name, ID: c.Name, Region: c.Location,
			Fields: []cloud.Field{
				{Key: "LOCATION", Value: c.Location},
				{Key: "VERSION", Value: c.CurrentMasterVersion},
				{Key: "STATUS", Value: c.Status},
				{Key: "NODES", Value: fmt.Sprintf("%d", c.CurrentNodeCount)},
			},
			Console: fmt.Sprintf("https://console.cloud.google.com/kubernetes/clusters/details/%s/%s?project=%s", c.Location, c.Name, p),
		})
	}
	return out, nil
}

func (g *GCP) fetchRun(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	p, err := g.requireProject(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := g.runSvc(ctx)
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	parent := fmt.Sprintf("projects/%s/locations/-", p)
	resp, err := svc.Projects.Locations.Services.List(parent).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	for _, s := range resp.Items {
		name, loc := "-", "-"
		uid := ""
		if s.Metadata != nil {
			name = orDefault(s.Metadata.Name, "-")
			loc = orDefault(s.Metadata.Namespace, "-")
			uid = s.Metadata.Uid
		}
		url, ready := "-", "-"
		if s.Status != nil {
			url = orDefault(s.Status.Url, "-")
			for _, cond := range s.Status.Conditions {
				if cond.Type == "Ready" {
					ready = orDefault(cond.Status, "-")
				}
			}
		}
		out = append(out, cloud.Resource{
			Kind: "run/service", Name: name, ID: uid, Region: loc,
			Fields: []cloud.Field{
				{Key: "REGION", Value: loc},
				{Key: "READY", Value: ready},
				{Key: "URL", Value: cloud.Trunc(url, 48)},
			},
			Console: fmt.Sprintf("https://console.cloud.google.com/run/detail/%s/%s/?project=%s", loc, name, p),
		})
	}
	return out, nil
}

func (g *GCP) fetchFunctions(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	p, err := g.requireProject(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := g.functionsSvc(ctx)
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	parent := fmt.Sprintf("projects/%s/locations/-", p)
	if err := svc.Projects.Locations.Functions.List(parent).Pages(ctx, func(page *cloudfunctions.ListFunctionsResponse) error {
		for _, f := range page.Functions {
			parts := strings.Split(f.Name, "/")
			loc, name := "-", "-"
			if len(parts) > 3 {
				loc = parts[3]
			}
			if len(parts) > 0 {
				name = parts[len(parts)-1]
			}
			runtime, state, env, mem := "-", "-", "-", "-"
			if f.BuildConfig != nil {
				runtime = f.BuildConfig.Runtime
			}
			if f.ServiceConfig != nil {
				mem = f.ServiceConfig.AvailableMemory
			}
			state = f.State
			env = f.Environment
			out = append(out, cloud.Resource{
				Kind: "fn/function", Name: name, ID: cloud.Short(f.Name), Region: loc,
				Fields: []cloud.Field{
					{Key: "LOCATION", Value: loc},
					{Key: "STATE", Value: state},
					{Key: "RUNTIME", Value: runtime},
					{Key: "ENV", Value: env},
					{Key: "MEMORY", Value: mem},
				},
				Console: fmt.Sprintf("https://console.cloud.google.com/functions/details/%s/%s?project=%s", loc, name, p),
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GCP) fetchBQ(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	p, err := g.requireProject(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := g.bqSvc(ctx)
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	if err := svc.Datasets.List(p).Pages(ctx, func(page *bigquery.DatasetList) error {
		for _, d := range page.Datasets {
			ref := "-"
			if d.DatasetReference != nil {
				ref = d.DatasetReference.DatasetId
			}
			label := d.FriendlyName
			if label == "" {
				label = "-"
			}
			out = append(out, cloud.Resource{
				Kind: "bq/dataset", Name: ref, ID: d.Id, Region: d.Location,
				Fields: []cloud.Field{
					{Key: "LOCATION", Value: d.Location},
					{Key: "LABEL", Value: label},
				},
				Console: fmt.Sprintf("https://console.cloud.google.com/bigquery?project=%s&d=%s", p, ref),
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GCP) fetchPubSub(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	p, err := g.requireProject(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := g.pubsubSvc(ctx)
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	if err := svc.Projects.Topics.List(fmt.Sprintf("projects/%s", p)).Pages(ctx, func(page *pubsub.ListTopicsResponse) error {
		for _, t := range page.Topics {
			full := t.Name
			out = append(out, cloud.Resource{
				Kind:    "pubsub/topic",
				Name:    cloud.Short(full),
				ID:      full,
				Console: fmt.Sprintf("https://console.cloud.google.com/cloudpubsub/topic/%s?project=%s", cloud.Short(full), p),
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *GCP) fetchSQL(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	p, err := g.requireProject(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := g.sqlSvc(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := svc.Instances.List(p).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	for _, i := range resp.Items {
		tier := "-"
		if i.Settings != nil {
			tier = lastSegment(i.Settings.Tier)
		}
		name := i.Name
		out = append(out, cloud.Resource{
			Kind: "sql/instance", Name: name, ID: name, Region: i.Region,
			Fields: []cloud.Field{
				{Key: "REGION", Value: i.Region},
				{Key: "STATE", Value: i.State},
				{Key: "VERSION", Value: i.DatabaseVersion},
				{Key: "TIER", Value: tier},
			},
			Console: fmt.Sprintf("https://console.cloud.google.com/sql/instances/%s/overview?project=%s", name, p),
		})
	}
	return out, nil
}

func (g *GCP) fetchGLogs(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	p, err := g.requireProject(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := g.loggingSvc(ctx)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().Add(-6 * time.Hour).Format("2006-01-02T15:04:05Z")
	resp, err := svc.Entries.List(&logging.ListLogEntriesRequest{
		ResourceNames: []string{fmt.Sprintf("projects/%s", p)},
		Filter:        fmt.Sprintf("timestamp >= %q", cutoff),
		OrderBy:       "timestamp desc",
		PageSize:      100,
	}).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	for _, e := range resp.Entries {
		msg := entryMessage(e)
		ts := e.Timestamp
		if t, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			ts = t.Local().Format("2006-01-02 15:04:05")
		}
		out = append(out, cloud.Resource{
			Kind: "log/entry", Name: cloud.Trunc(msg, 120), ID: e.InsertId,
			Fields: []cloud.Field{
				{Key: "TIME", Value: ts},
				{Key: "SEVERITY", Value: e.Severity},
				{Key: "LOG", Value: cloud.Short(e.LogName)},
			},
			Detail: fmt.Sprintf("log:      %s\ntime:     %s\nseverity: %s\n\n%s", e.LogName, ts, e.Severity, msg),
		})
	}
	return out, nil
}

func entryMessage(e *logging.LogEntry) string {
	if e.TextPayload != "" {
		return e.TextPayload
	}
	if len(e.JsonPayload) > 0 {
		var m map[string]any
		if err := json.Unmarshal(e.JsonPayload, &m); err == nil {
			return flattenMap(m)
		}
		return string(e.JsonPayload)
	}
	if len(e.ProtoPayload) > 0 {
		var m map[string]any
		if err := json.Unmarshal(e.ProtoPayload, &m); err == nil {
			if t, ok := m["@type"].(string); ok {
				return t
			}
		}
		return string(e.ProtoPayload)
	}
	if e.Resource != nil && e.Resource.Type != "" {
		return e.Resource.Type
	}
	return "(no payload)"
}

func flattenMap(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%s=%v", k, m[k])
	}
	return b.String()
}
