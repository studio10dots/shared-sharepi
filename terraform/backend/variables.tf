variable "project_id" {
  description = "Your GCP project ID (billing must be enabled)."
  type        = string

  # Same rule as Google's: 6-30 characters, lowercase letters, digits and
  # hyphens, starting with a letter and not ending with a hyphen.
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "project_id must be the project's ID (6-30 lowercase letters, digits or hyphens), not its display name."
  }
}

variable "region" {
  description = "Region for the bucket and the Cloud Run service."
  type        = string
  default     = "asia-northeast1"
}

variable "backend_image" {
  description = "Container image of the backend, published by the project maintainer (publisher.auto.tfvars), as ghcr.io/<publisher>/<name>:<version>. Cloud Run pulls it straight from GHCR (observed to work, docs/BACKEND_DESIGN.md section 12; not documented as supported by Google). Use a fixed version tag."
  type        = string

  validation {
    condition     = !startswith(var.backend_image, "REPLACE_WITH")
    error_message = "backend_image is not set in publisher.auto.tfvars yet: the app's publisher has to fill it in."
  }
}

variable "google_client_id" {
  description = "The app publisher's Web OAuth client ID. Public; the backend only accepts ID tokens issued for it (publisher.auto.tfvars)."
  type        = string

  validation {
    condition     = !startswith(var.google_client_id, "REPLACE_WITH")
    error_message = "google_client_id is not set in publisher.auto.tfvars yet: the app's publisher has to fill it in."
  }
}

variable "bucket_name" {
  description = "Globally unique bucket name. Defaults to \"<project_id>-photos\"."
  type        = string
  default     = null
}

variable "admin_emails" {
  description = "Google accounts that may create groups and invite people. Defaults to the account running Terraform. Ignored by the backend when admin_subs is set."
  type        = list(string)
  default     = []
}

variable "admin_subs" {
  description = <<-EOT
    Google user ids (the `sub` of the account's ID token, e.g. "110571000531995686849")
    of the administrators. When set, the backend decides who is an administrator by
    these ids alone and ignores admin_emails: an email address can change hands,
    a user id never does. setup.sh fills in the id of the account running it.
    Empty (the default) keeps the email check.
  EOT
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for s in var.admin_subs : can(regex("^[0-9]{10,30}$", s))])
    error_message = "admin_subs must be Google user ids: 10-30 digits each (not email addresses)."
  }
}

variable "max_instances" {
  description = "Upper bound on Cloud Run instances. Caps the bill if the public endpoint is flooded."
  type        = number
  default     = 3
}

variable "deletion_protection" {
  description = "Refuse `terraform destroy` of the Cloud Run services (the provider's own default since 6.x), as a guard against deleting a live backend by mistake. To remove them, set this to false, run `terraform apply`, then `terraform destroy`: the value is read from the saved state, so passing false only on the destroy command is not enough."
  type        = bool
  default     = true
}

variable "require_token_binding" {
  description = "Refuse sign-in tokens that are not bound to this backend (docs/BACKEND_DESIGN.md, \"Token binding\"). A token bound to another backend is always refused; this also refuses one with no binding. Leave false until the app sends bound tokens."
  type        = bool
  default     = false
}

variable "soft_delete_retention_seconds" {
  description = "How long a deleted photo stays recoverable (default 30 days, GCS maximum 90)."
  type        = number
  default     = 2592000
}

variable "enable_web" {
  description = "Also host the Flutter Web build on Cloud Run (optional, off by default)."
  type        = bool
  default     = false
}

variable "web_image" {
  description = "Container image of the Web static build, published by the project maintainer. Required when enable_web is true."
  type        = string
  default     = null
}

variable "web_origin_override" {
  description = "Override for the Web build's own origin, if Cloud Run's predictable URL format (https://sharepi-web-<project number>.<region>.run.app) ever does not hold for your project. Only used when enable_web is true."
  type        = string
  default     = null
}

variable "log_retention_days" {
  description = "How long Cloud Logging keeps this project's logs, including Cloud Run request logs (which contain callers' IP addresses). 30 is Google's default; raise it if you need to answer legal requests later (1-3650)."
  type        = number
  default     = 30

  validation {
    condition     = var.log_retention_days >= 1 && var.log_retention_days <= 3650
    error_message = "log_retention_days must be between 1 and 3650."
  }
}
