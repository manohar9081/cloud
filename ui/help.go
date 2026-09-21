package ui

import "fmt"

func helpContent() string {
	return fmt.Sprintf(` clouds v%s — k9s-style browser for AWS & GCP

 NAVIGATION
  enter        open detail (or drill down: S3 objects, ECS services, log events)
  esc / q      back — q at the top level quits
  :<service>   jump to a service (see list below), e.g. :ec2 :s3 :lambda
  :aws/:gcp    switch cloud provider
  /            filter rows (live substring match, esc to clear)
  s / S        sort — s cycles the sort column, S flips ascending vs descending
  p            toggle provider (aws <-> gcp)
  1 / 2        jump to aws / gcp
  r            refresh the current view
  d            open the selected resource in the web console
  g            download the selected S3/GCS object to disk (folders: recursive)
  y            copy the selected resource name to the clipboard
  ?            this help
  :q           quit

 AWS SERVICES
  :ec2  :s3  :lambda  :rds  :logs  :ecs  :eks  :sqs  :dynamodb (ddb)

 GCP SERVICES
  :gce (compute)  :gcs  :gke  :run  :fn (functions)  :bq  :pubsub  :sql  :glogs

 AUTH
  AWS  default credential chain (~/.aws, env vars, SSO, IMDS).
       Overrides: --profile, --region
  GCP  Application Default Credentials — run: gcloud auth application-default login
       Project: --project flag or GOOGLE_CLOUD_PROJECT env var
`, Version)
}
