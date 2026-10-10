variable "region" {
  description = "AWS region for the bucket and the Lambda function."
  type        = string
  default     = "ap-northeast-1"
}

variable "backend_image" {
  description = <<-EOT
    Container image of the backend, as an Amazon ECR image URI
    (e.g. "111122223333.dkr.ecr.ap-northeast-1.amazonaws.com/chamagon-backend:1").
    AWS Lambda's container support only pulls from ECR, unlike Cloud Run: a
    registry such as Docker Hub or GHCR does not work here. The maintainer's
    published image needs an ECR repository (their own, or the public ECR
    Gallery) with a policy that allows pulling it across accounts.

    Set exactly one of backend_image and backend_binary. Use backend_binary
    where ECR cannot be used (an organization's service control policy may deny
    it, as one did in the first test).
  EOT
  type        = string
  default     = null
}

variable "backend_binary" {
  description = <<-EOT
    Path to the backend built for Lambda instead of a container image:
    `cd backend && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o bootstrap .`
    Terraform zips it (with the executable bit set, which a zip made on Windows
    would lose) and runs it on the provided.al2023 runtime behind the AWS Lambda
    Web Adapter layer. It is the same program as the container image.
  EOT
  type        = string
  default     = null
}

variable "web_adapter_layer_version" {
  description = "Version of AWS's public Lambda Web Adapter layer (LambdaAdapterLayerX86, account 753240598075) used with backend_binary. See https://github.com/awslabs/aws-lambda-web-adapter for the current one."
  type        = number
  default     = 30
}

variable "environment" {
  description = "Which of the app's environments this backend serves: dev or prod. It picks config/<environment>.json, the one place the Web OAuth client ID is kept (same as terraform/backend)."
  type        = string
  default     = "prod"

  validation {
    condition     = contains(["dev", "prod"], var.environment)
    error_message = "environment must be \"dev\" or \"prod\"."
  }
}

variable "google_client_id" {
  description = "Overrides the Web OAuth client ID that is otherwise read from config/<environment>.json. Leave it unset: the file is the single source, so the backend always accepts the same client the app signs in with."
  type        = string
  default     = null
}

variable "bucket_name" {
  description = "Globally unique bucket name. Defaults to \"chamagon-photos-<account id>\"."
  type        = string
  default     = null
}

variable "admin_emails" {
  description = <<-EOT
    Google accounts that may create groups and invite people, used only when
    admin_subs is empty. Unlike the GCP module there is no "account running
    terraform" email to default to (an AWS IAM principal has no Google identity),
    so the owner has to be named: by admin_subs (preferred) or by this list. The
    backend refuses to be created with neither (see the Lambda's precondition).
  EOT
  type        = list(string)
  default     = []
}

variable "admin_subs" {
  description = <<-EOT
    Google user ids (the `sub` of the account's ID token) of the administrators.
    When set, the backend decides who is an administrator by these ids alone and
    ignores admin_emails: an email address can change hands, a user id never does.
    Empty (the default) keeps the email check.
  EOT
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for s in var.admin_subs : can(regex("^[0-9]{10,30}$", s))])
    error_message = "admin_subs must be Google user ids: 10-30 digits each (not email addresses)."
  }
}

variable "require_token_binding" {
  description = "Refuse sign-in tokens that are not bound to this backend (docs/BACKEND_DESIGN.md, \"Token binding\"). A token bound to another backend is always refused; this also refuses one with no binding. Leave false until the app sends bound tokens (same as terraform/backend)."
  type        = bool
  default     = false
}

variable "max_concurrency" {
  description = <<-EOT
    Upper bound on concurrent Lambda executions (reserved concurrency): caps the
    bill if the public endpoint is flooded, like max_instances in the GCP module.
    Null (the default) reserves nothing. A new AWS account has a low account-wide
    limit (10) and Lambda refuses a reservation that would leave fewer than 10
    unreserved, so no reservation is possible until the limit is raised
    (Service Quotas -> Lambda -> Concurrent executions); then set e.g. 3.
  EOT
  type        = number
  default     = null
}

variable "memory_size" {
  description = "Lambda memory, in MB (also proportionally sets CPU). 128 mirrors the GCP module's Cloud Run allocation; raise it if cold starts are too slow."
  type        = number
  default     = 128
}

variable "timeout_seconds" {
  description = "Per-request Lambda timeout. Photo bytes never pass through the backend, so every request is small and quick (Agents.md section 4)."
  type        = number
  default     = 30
}

variable "soft_delete_retention_days" {
  description = "How long a deleted photo stays recoverable as a noncurrent S3 object version (default 30 days; mirrors soft_delete_retention_seconds = 2592000 in the GCP module)."
  type        = number
  default     = 30
}

variable "log_retention_days" {
  description = "How long CloudWatch Logs keeps this function's request logs (which contain callers' IP addresses). 1-3653, default 30."
  type        = number
  default     = 30

  validation {
    condition     = var.log_retention_days >= 1 && var.log_retention_days <= 3653
    error_message = "log_retention_days must be between 1 and 3653."
  }
}
