// Package providers implements the cloud provider backends (AWS via
// aws-sdk-go-v2, GCP via google.golang.org/api, plus a demo dataset) behind
// a small common interface.
package providers

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ectypes "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqsTypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"clouds/cloud"
)

// Impl is a concrete provider backend (AWS, GCP, demo variants).
type Impl interface {
	ID() string
	Info(ctx context.Context) string
	Catalog() cloud.Provider
}

// BuildAll returns the provider backends selected by the options
// (demo dataset or the real clouds).
func BuildAll(opts cloud.Options) []Impl {
	if opts.Demo {
		return []Impl{newDemoAWS(), newDemoGCP()}
	}
	return []Impl{NewAWS(opts), NewGCP(opts)}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ------------------------------------------------------------------- AWS ---

// AWS is the Amazon Web Services backend.
type AWS struct {
	opts    cloud.Options
	cfg     aws.Config
	initErr error

	mu      sync.Mutex
	account string
}

// NewAWS loads the AWS config (standard credential chain).
func NewAWS(opts cloud.Options) *AWS {
	var optFns []func(*config.LoadOptions) error
	if opts.Profile != "" {
		optFns = append(optFns, config.WithSharedConfigProfile(opts.Profile))
	}
	if opts.Region != "" {
		optFns = append(optFns, config.WithRegion(opts.Region))
	}
	cfg, err := config.LoadDefaultConfig(context.Background(), optFns...)
	return &AWS{opts: opts, cfg: cfg, initErr: err}
}

// ID implements Impl.
func (a *AWS) ID() string { return "aws" }

func (a *AWS) region() string {
	if a.cfg.Region == "" {
		return "?"
	}
	return a.cfg.Region
}

// Info implements Impl (header line for the TUI topbar).
func (a *AWS) Info(ctx context.Context) string {
	a.mu.Lock()
	acct := a.account
	a.mu.Unlock()
	if acct == "" {
		out, err := sts.NewFromConfig(a.cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if err == nil && out.Account != nil {
			acct = *out.Account
		} else {
			acct = "?"
		}
		a.mu.Lock()
		a.account = acct
		a.mu.Unlock()
	}
	return fmt.Sprintf("profile=%s account=%s region=%s",
		orDefault(a.opts.Profile, "default"), acct, a.region())
}

// Catalog implements Impl.
func (a *AWS) Catalog() cloud.Provider {
	return cloud.Provider{
		ID:   "aws",
		Name: "Amazon Web Services",
		Services: []cloud.Service{
			{ID: "ec2", Name: "EC2 Instances", Fetch: a.fetchEC2},
			{ID: "s3", Name: "S3 Buckets", Fetch: a.fetchS3},
			{ID: "lambda", Name: "Lambda Functions", Fetch: a.fetchLambda},
			{ID: "rds", Name: "RDS Instances", Fetch: a.fetchRDS},
			{ID: "logs", Name: "CloudWatch Log Groups", Alias: "log", Fetch: a.fetchLogs},
			{ID: "ecs", Name: "ECS Clusters", Fetch: a.fetchECS},
			{ID: "eks", Name: "EKS Clusters", Fetch: a.fetchEKS},
			{ID: "sqs", Name: "SQS Queues", Fetch: a.fetchSQS},
			{ID: "dynamodb", Name: "DynamoDB Tables", Alias: "ddb", Fetch: a.fetchDynamoDB},
		},
	}
}

func tagValue(tags []ectypes.Tag, key string) string {
	for _, t := range tags {
		if t.Key != nil && *t.Key == key && t.Value != nil {
			return *t.Value
		}
	}
	return ""
}

func (a *AWS) fetchEC2(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	c := ec2.NewFromConfig(a.cfg)
	var out []cloud.Resource
	p := ec2.NewDescribeInstancesPaginator(c, &ec2.DescribeInstancesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, res := range page.Reservations {
			for _, i := range res.Instances {
				if len(out) >= cloud.MaxRows {
					return out, nil
				}
				id := aws.ToString(i.InstanceId)
				state := "-"
				if i.State != nil && i.State.Name != "" {
					state = string(i.State.Name)
				}
				pub, priv := "-", "-"
				if i.PublicIpAddress != nil {
					pub = *i.PublicIpAddress
				}
				if i.PrivateIpAddress != nil {
					priv = *i.PrivateIpAddress
				}
				az := "-"
				if i.Placement != nil && i.Placement.AvailabilityZone != nil {
					az = *i.Placement.AvailabilityZone
				}
				name := tagValue(i.Tags, "Name")
				if name == "" {
					name = id
				}
				launched := "-"
				if i.LaunchTime != nil {
					launched = cloud.Ago(*i.LaunchTime)
				}
				out = append(out, cloud.Resource{
					Kind: "ec2/instance", Name: name, ID: id, Region: a.region(),
					Fields: []cloud.Field{
						{Key: "STATE", Value: state},
						{Key: "TYPE", Value: string(i.InstanceType)},
						{Key: "PUBLIC IP", Value: pub},
						{Key: "PRIVATE IP", Value: priv},
						{Key: "AZ", Value: az},
						{Key: "LAUNCHED", Value: launched},
					},
					Console: fmt.Sprintf("https://console.aws.amazon.com/ec2/home?region=%s#InstanceDetails:instanceId=%s", a.region(), id),
				})
			}
		}
	}
	return out, nil
}

func (a *AWS) fetchS3(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	c := s3.NewFromConfig(a.cfg)
	out, err := c.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}
	bs := out.Buckets
	if len(bs) > 200 {
		bs = bs[:200]
	}
	regions := make([]string, len(bs))
	cloud.ParallelRange(len(bs), 10, func(i int) {
		region := "us-east-1"
		loc, err := c.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: bs[i].Name})
		if err == nil && loc.LocationConstraint != "" {
			region = string(loc.LocationConstraint)
			if region == "EU" {
				region = "eu-west-1"
			}
		}
		regions[i] = region
	})
	res := make([]cloud.Resource, 0, len(bs))
	for i, b := range bs {
		name := aws.ToString(b.Name)
		created := "-"
		if b.CreationDate != nil {
			created = cloud.Ago(*b.CreationDate)
		}
		bucket := name
		res = append(res, cloud.Resource{
			Kind: "s3/bucket", Name: name, ID: "s3://" + name, Region: regions[i],
			Fields: []cloud.Field{
				{Key: "REGION", Value: regions[i]},
				{Key: "CREATED", Value: created},
			},
			Console: fmt.Sprintf("https://s3.console.aws.amazon.com/s3/buckets/%s?region=%s", name, a.region()),
			Sub: func(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
				return a.s3Objects(ctx, bucket)
			},
		})
	}
	return res, nil
}

func (a *AWS) s3Objects(ctx context.Context, bucket string) ([]cloud.Resource, error) {
	c := s3.NewFromConfig(a.cfg)
	var out []cloud.Resource
	p := s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			if len(out) >= 500 {
				return out, nil
			}
			key := aws.ToString(o.Key)
			size := aws.ToInt64(o.Size)
			var dl *cloud.Download
			if strings.HasSuffix(key, "/") {
				dl = a.makePrefixDownload(bucket, key)
			} else {
				dl = a.makeDownload(bucket, key, size)
			}
			out = append(out, cloud.Resource{
				Kind: "s3/object", Name: key, ID: cloud.HumanSize(size),
				Fields: []cloud.Field{
					{Key: "STORAGE", Value: string(o.StorageClass)},
					{Key: "MODIFIED", Value: cloud.Ago(aws.ToTime(o.LastModified))},
				},
				Detail: fmt.Sprintf("bucket:   %s\nkey:      %s\nsize:     %s\nmodified: %s",
					bucket, key, cloud.HumanSize(size), cloud.FmtTS(aws.ToTime(o.LastModified))),
				Console:  fmt.Sprintf("https://s3.console.aws.amazon.com/s3/buckets/%s?prefix=%s", bucket, key),
				Download: dl,
			})
		}
	}
	return out, nil
}

func (a *AWS) makeDownload(bucket, key string, size int64) *cloud.Download {
	return &cloud.Download{
		Label: "s3 object",
		Run: func(ctx context.Context, opts cloud.Options, progress func(string)) (string, error) {
			target := cloud.SafePath(opts.DownloadDir, bucket, key)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			c := s3.NewFromConfig(a.cfg)
			out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
			if err != nil {
				return "", err
			}
			defer out.Body.Close()
			f, err := os.Create(target)
			if err != nil {
				return "", err
			}
			defer f.Close()
			total := size
			if out.ContentLength != nil && *out.ContentLength > 0 {
				total = *out.ContentLength
			}
			cw := &progressWriter{total: total, key: key, progress: progress}
			if _, err := io.Copy(cw, out.Body); err != nil {
				return "", err
			}
			return target, nil
		},
	}
}

func (a *AWS) makePrefixDownload(bucket, prefix string) *cloud.Download {
	return &cloud.Download{
		Label: "s3 folder (recursive, max 200 files)",
		Run: func(ctx context.Context, opts cloud.Options, progress func(string)) (string, error) {
			c := s3.NewFromConfig(a.cfg)
			var keys []string
			p := s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{
				Bucket: aws.String(bucket), Prefix: aws.String(prefix),
			})
			for p.HasMorePages() && len(keys) < 200 {
				page, err := p.NextPage(ctx)
				if err != nil {
					return "", err
				}
				for _, o := range page.Contents {
					k := aws.ToString(o.Key)
					if k != "" && !strings.HasSuffix(k, "/") && aws.ToInt64(o.Size) > 0 {
						keys = append(keys, k)
					}
					if len(keys) >= 200 {
						break
					}
				}
			}
			for i, k := range keys {
				target := cloud.SafePath(opts.DownloadDir, bucket, k)
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return "", err
				}
				progress(fmt.Sprintf("[%d/%d] %s", i+1, len(keys), cloud.Trunc(k, 44)))
				out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(k)})
				if err != nil {
					return "", err
				}
				f, err := os.Create(target)
				if err != nil {
					out.Body.Close()
					return "", err
				}
				_, copyErr := io.Copy(f, out.Body)
				out.Body.Close()
				f.Close()
				if copyErr != nil {
					return "", copyErr
				}
			}
			return cloud.SafePath(opts.DownloadDir, bucket, prefix), nil
		},
	}
}

// progressWriter throttles progress callbacks to ~7/sec.
type progressWriter struct {
	n, total int64
	key      string
	last     time.Time
	progress func(string)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	if time.Since(w.last) > 150*time.Millisecond {
		w.last = time.Now()
		w.progress(fmt.Sprintf("%s / %s  %s",
			cloud.HumanSize(w.n), cloud.HumanSize(w.total), cloud.Trunc(w.key, 40)))
	}
	return len(p), nil
}

func (a *AWS) fetchLambda(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	c := lambda.NewFromConfig(a.cfg)
	var out []cloud.Resource
	p := lambda.NewListFunctionsPaginator(c, &lambda.ListFunctionsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, f := range page.Functions {
			if len(out) >= cloud.MaxRows {
				return out, nil
			}
			name := aws.ToString(f.FunctionName)
			arn := aws.ToString(f.FunctionArn)
			modified := "-"
			if f.LastModified != nil {
				if t, err := time.Parse(time.RFC3339, *f.LastModified); err == nil {
					modified = cloud.Ago(t)
				}
			}
			out = append(out, cloud.Resource{
				Kind: "lambda/function", Name: name, ID: cloud.Short(arn), Region: a.region(),
				Fields: []cloud.Field{
					{Key: "RUNTIME", Value: string(f.Runtime)},
					{Key: "MEMORY", Value: fmt.Sprintf("%d MB", f.MemorySize)},
					{Key: "TIMEOUT", Value: fmt.Sprintf("%ds", f.Timeout)},
					{Key: "HANDLER", Value: aws.ToString(f.Handler)},
					{Key: "MODIFIED", Value: modified},
				},
				Console: fmt.Sprintf("https://console.aws.amazon.com/lambda/home?region=%s#/functions/%s", a.region(), name),
			})
		}
	}
	return out, nil
}

func (a *AWS) fetchRDS(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	c := rds.NewFromConfig(a.cfg)
	var out []cloud.Resource
	p := rds.NewDescribeDBInstancesPaginator(c, &rds.DescribeDBInstancesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, db := range page.DBInstances {
			if len(out) >= cloud.MaxRows {
				return out, nil
			}
			ident := aws.ToString(db.DBInstanceIdentifier)
			engine := strings.TrimSpace(aws.ToString(db.Engine) + " " + aws.ToString(db.EngineVersion))
			endpoint := "-"
			if db.Endpoint != nil && db.Endpoint.Address != nil {
				endpoint = *db.Endpoint.Address
			}
			multiAZ := "no"
			if aws.ToBool(db.MultiAZ) {
				multiAZ = "yes"
			}
			out = append(out, cloud.Resource{
				Kind: "rds/instance", Name: ident, ID: ident, Region: a.region(),
				Fields: []cloud.Field{
					{Key: "STATUS", Value: aws.ToString(db.DBInstanceStatus)},
					{Key: "ENGINE", Value: engine},
					{Key: "CLASS", Value: aws.ToString(db.DBInstanceClass)},
					{Key: "ENDPOINT", Value: endpoint},
					{Key: "MULTI-AZ", Value: multiAZ},
				},
				Console: fmt.Sprintf("https://console.aws.amazon.com/rds/home?region=%s#database:id=%s", a.region(), ident),
			})
		}
	}
	return out, nil
}

func (a *AWS) fetchLogs(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	c := cloudwatchlogs.NewFromConfig(a.cfg)
	var out []cloud.Resource
	p := cloudwatchlogs.NewDescribeLogGroupsPaginator(c, &cloudwatchlogs.DescribeLogGroupsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range page.LogGroups {
			if len(out) >= cloud.MaxRows {
				return out, nil
			}
			name := aws.ToString(g.LogGroupName)
			retention := "never"
			if g.RetentionInDays != nil {
				retention = fmt.Sprintf("%dd", *g.RetentionInDays)
			}
			group := name
			out = append(out, cloud.Resource{
				Kind: "logs/group", Name: name, ID: name, Region: a.region(),
				Fields: []cloud.Field{
					{Key: "STORED", Value: cloud.HumanSize(aws.ToInt64(g.StoredBytes))},
					{Key: "RETENTION", Value: retention},
				},
				Console: fmt.Sprintf("https://console.aws.amazon.com/cloudwatch/home?region=%s#logsV2:log-groups", a.region()),
				Sub: func(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
					return a.logEvents(ctx, group)
				},
			})
		}
	}
	return out, nil
}

func (a *AWS) logEvents(ctx context.Context, group string) ([]cloud.Resource, error) {
	c := cloudwatchlogs.NewFromConfig(a.cfg)
	out, err := c.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName: aws.String(group),
		Limit:        aws.Int32(200),
	})
	if err != nil {
		return nil, err
	}
	events := out.Events
	var rows []cloud.Resource
	for i := len(events) - 1; i >= 0; i-- { // newest first
		e := events[i]
		msg := aws.ToString(e.Message)
		ts := "-"
		if e.Timestamp != nil {
			ts = cloud.FmtTS(time.UnixMilli(*e.Timestamp))
		}
		stream := aws.ToString(e.LogStreamName)
		rows = append(rows, cloud.Resource{
			Kind: "logs/event", Name: cloud.Trunc(msg, 120), ID: aws.ToString(e.EventId), Region: a.region(),
			Fields: []cloud.Field{
				{Key: "TIME", Value: ts},
				{Key: "STREAM", Value: cloud.Short(stream)},
			},
			Detail: fmt.Sprintf("stream:   %s\ntime:     %s\n\n%s", stream, ts, msg),
		})
	}
	return rows, nil
}

func (a *AWS) fetchECS(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	c := ecs.NewFromConfig(a.cfg)
	var arns []string
	p := ecs.NewListClustersPaginator(c, &ecs.ListClustersInput{})
	for p.HasMorePages() && len(arns) < 100 {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		arns = append(arns, page.ClusterArns...)
		if len(arns) >= 100 {
			arns = arns[:100]
		}
	}
	var out []cloud.Resource
	for i := 0; i < len(arns); i += 100 {
		end := min(i+100, len(arns))
		resp, err := c.DescribeClusters(ctx, &ecs.DescribeClustersInput{Clusters: arns[i:end]})
		if err != nil {
			return nil, err
		}
		for _, cl := range resp.Clusters {
			name := aws.ToString(cl.ClusterName)
			arn := aws.ToString(cl.ClusterArn)
			out = append(out, cloud.Resource{
				Kind: "ecs/cluster", Name: name, ID: cloud.Short(arn), Region: a.region(),
				Fields: []cloud.Field{
					{Key: "STATUS", Value: aws.ToString(cl.Status)},
					{Key: "RUNNING", Value: fmt.Sprintf("%d", cl.RunningTasksCount)},
					{Key: "PENDING", Value: fmt.Sprintf("%d", cl.PendingTasksCount)},
					{Key: "SERVICES", Value: fmt.Sprintf("%d", cl.ActiveServicesCount)},
				},
				Console: fmt.Sprintf("https://console.aws.amazon.com/ecs/v2/clusters/%s/services?region=%s", name, a.region()),
				Sub: func(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
					return a.ecsServices(ctx, arn)
				},
			})
		}
	}
	return out, nil
}

func (a *AWS) ecsServices(ctx context.Context, clusterArn string) ([]cloud.Resource, error) {
	c := ecs.NewFromConfig(a.cfg)
	var sarns []string
	p := ecs.NewListServicesPaginator(c, &ecs.ListServicesInput{Cluster: aws.String(clusterArn)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		sarns = append(sarns, page.ServiceArns...)
	}
	cluster := cloud.Short(clusterArn)
	var out []cloud.Resource
	for i := 0; i < len(sarns); i += 10 {
		end := min(i+10, len(sarns))
		resp, err := c.DescribeServices(ctx, &ecs.DescribeServicesInput{
			Cluster: aws.String(clusterArn), Services: sarns[i:end],
		})
		if err != nil {
			return nil, err
		}
		for _, s := range resp.Services {
			name := aws.ToString(s.ServiceName)
			out = append(out, cloud.Resource{
				Kind: "ecs/service", Name: name, ID: cloud.Short(aws.ToString(s.ServiceArn)), Region: a.region(),
				Fields: []cloud.Field{
					{Key: "STATUS", Value: aws.ToString(s.Status)},
					{Key: "DESIRED", Value: fmt.Sprintf("%d", s.DesiredCount)},
					{Key: "RUNNING", Value: fmt.Sprintf("%d", s.RunningCount)},
					{Key: "LAUNCH", Value: string(s.LaunchType)},
				},
				Console: fmt.Sprintf("https://console.aws.amazon.com/ecs/v2/clusters/%s/services/%s?region=%s", cluster, name, a.region()),
			})
		}
	}
	return out, nil
}

func (a *AWS) fetchEKS(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	c := eks.NewFromConfig(a.cfg)
	var names []string
	p := eks.NewListClustersPaginator(c, &eks.ListClustersInput{})
	for p.HasMorePages() && len(names) < 100 {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		names = append(names, page.Clusters...)
		if len(names) >= 100 {
			names = names[:100]
		}
	}
	clusters := make([]*eks.DescribeClusterOutput, len(names))
	cloud.ParallelRange(len(names), 8, func(i int) {
		if out, err := c.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(names[i])}); err == nil {
			clusters[i] = out
		}
	})
	var out []cloud.Resource
	for _, co := range clusters {
		if co == nil || co.Cluster == nil {
			continue
		}
		cl := co.Cluster
		name := aws.ToString(cl.Name)
		created := "-"
		if cl.CreatedAt != nil {
			created = cloud.Ago(*cl.CreatedAt)
		}
		out = append(out, cloud.Resource{
			Kind: "eks/cluster", Name: name, ID: name, Region: a.region(),
			Fields: []cloud.Field{
				{Key: "VERSION", Value: aws.ToString(cl.Version)},
				{Key: "STATUS", Value: string(cl.Status)},
				{Key: "PLATFORM", Value: aws.ToString(cl.PlatformVersion)},
				{Key: "CREATED", Value: created},
			},
			Console: fmt.Sprintf("https://console.aws.amazon.com/eks/home?region=%s#/clusters/%s", a.region(), name),
		})
	}
	return out, nil
}

func (a *AWS) fetchSQS(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	c := sqs.NewFromConfig(a.cfg)
	var urls []string
	p := sqs.NewListQueuesPaginator(c, &sqs.ListQueuesInput{MaxResults: aws.Int32(50)})
	for p.HasMorePages() && len(urls) < 200 {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		urls = append(urls, page.QueueUrls...)
		if len(urls) >= 200 {
			urls = urls[:200]
		}
	}
	attrs := make([]map[string]string, len(urls))
	cloud.ParallelRange(len(urls), 8, func(i int) {
		if out, err := c.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       aws.String(urls[i]),
			AttributeNames: []sqsTypes.QueueAttributeName{sqsTypes.QueueAttributeNameAll},
		}); err == nil {
			attrs[i] = out.Attributes
		}
	})
	var out []cloud.Resource
	for i, u := range urls {
		attr := attrs[i]
		created := "-"
		if ts, ok := attr["CreatedTimestamp"]; ok {
			if n, perr := strconv.ParseInt(ts, 10, 64); perr == nil {
				created = cloud.FmtTS(time.Unix(n, 0))
			}
		}
		out = append(out, cloud.Resource{
			Kind: "sqs/queue", Name: cloud.Short(u), ID: u, Region: a.region(),
			Fields: []cloud.Field{
				{Key: "MSGS", Value: attr["ApproximateNumberOfMessages"]},
				{Key: "IN-FLIGHT", Value: attr["ApproximateNumberOfMessagesNotVisible"]},
				{Key: "DELAYED", Value: attr["ApproximateNumberOfMessagesDelayed"]},
				{Key: "CREATED", Value: created},
			},
			Console: fmt.Sprintf("https://console.aws.amazon.com/sqs/v3/home?region=%s#/queues", a.region()),
		})
	}
	return out, nil
}

func (a *AWS) fetchDynamoDB(ctx context.Context, opts cloud.Options) ([]cloud.Resource, error) {
	if a.initErr != nil {
		return nil, a.initErr
	}
	c := dynamodb.NewFromConfig(a.cfg)
	var names []string
	p := dynamodb.NewListTablesPaginator(c, &dynamodb.ListTablesInput{})
	for p.HasMorePages() && len(names) < 200 {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		names = append(names, page.TableNames...)
		if len(names) >= 200 {
			names = names[:200]
		}
	}
	tables := make([]*dynamodb.DescribeTableOutput, len(names))
	cloud.ParallelRange(len(names), 8, func(i int) {
		if out, err := c.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(names[i])}); err == nil {
			tables[i] = out
		}
	})
	var out []cloud.Resource
	for _, to := range tables {
		if to == nil || to.Table == nil {
			continue
		}
		t := to.Table
		name := aws.ToString(t.TableName)
		arn := aws.ToString(t.TableArn)
		out = append(out, cloud.Resource{
			Kind: "dynamodb/table", Name: name, ID: arn, Region: a.region(),
			Fields: []cloud.Field{
				{Key: "STATUS", Value: string(t.TableStatus)},
				{Key: "ITEMS", Value: fmt.Sprintf("%d", t.ItemCount)},
				{Key: "SIZE", Value: cloud.HumanSize(aws.ToInt64(t.TableSizeBytes))},
			},
			Console: fmt.Sprintf("https://console.aws.amazon.com/dynamodbv2/home?region=%s#table?name=%s", a.region(), name),
		})
	}
	return out, nil
}
