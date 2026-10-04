data "google_client_openid_userinfo" "me" {}

data "google_project" "this" {
  project_id = var.project_id
}

locals {
  bucket_name = coalesce(var.bucket_name, "${var.project_id}-photos")
  # Whoever runs `terraform apply` holds the GCP contract, so they are the administrator.
  admin_emails = length(var.admin_emails) > 0 ? var.admin_emails : [lower(data.google_client_openid_userinfo.me.email)]

  # Cloud Run v2's default URL is predictable (https://SERVICE-PROJECT_NUMBER.REGION.run.app),
  # which lets the web and backend services each tell the other its URL without a
  # Terraform dependency cycle (each service's own real `.uri` attribute is only
  # known after the OTHER service already referenced it). web_origin_override
  # escapes this prediction if Google ever changes that format for your project.
  predicted_web_url = "https://chamagon-web-${data.google_project.this.number}.${var.region}.run.app"
  web_origin        = var.enable_web ? coalesce(var.web_origin_override, local.predicted_web_url) : ""
}

resource "google_project_service" "required" {
  for_each = toset([
    "run.googleapis.com",
    "storage.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
  ])

  service            = each.value
  disable_on_destroy = false
}

# One bucket for all groups. Only the backend's service account can reach it.
resource "google_storage_bucket" "photos" {
  name     = local.bucket_name
  location = var.region

  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"

  # Originals cool down as they stop being read; thumbnails (< 128 KiB) stay in Standard.
  autoclass {
    enabled = true
  }

  soft_delete_policy {
    retention_duration_seconds = var.soft_delete_retention_seconds
  }

  # Share-link manifests ("{group_id}/shares/{share_id}.share.json")
  # are access grants that already expire in minutes via their own signed URL;
  # this just clears the small objects themselves out of the bucket afterwards.
  # matchesSuffix, not matchesPrefix: group_id varies per group, so there is no
  # single prefix that covers every group's shares/ folder, but every manifest
  # shares this one suffix regardless of group.
  lifecycle_rule {
    condition {
      matches_suffix = [".share.json"]
      age            = 1
    }
    action {
      type = "Delete"
    }
  }

  depends_on = [google_project_service.required]
}

resource "google_service_account" "backend" {
  account_id   = "chamagon-backend"
  display_name = "sharepi backend"

  depends_on = [google_project_service.required]
}

resource "google_storage_bucket_iam_member" "backend_objects" {
  bucket = google_storage_bucket.photos.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.backend.email}"
}

# Lets the backend sign URLs as itself through signBlob, so no key file exists.
resource "google_service_account_iam_member" "backend_signs_as_self" {
  service_account_id = google_service_account.backend.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = "serviceAccount:${google_service_account.backend.email}"
}

resource "google_cloud_run_v2_service" "backend" {
  name     = "chamagon-backend"
  location = var.region

  # Callable from the internet: the backend authenticates every request itself
  # (Google ID token). Turning Cloud Run's own invoker IAM check off does this
  # without an `allUsers` role binding, which an organization's "Domain
  # restricted sharing" policy (iam.allowedPolicyMemberDomains, common on
  # Workspace) refuses ("users named in the policy do not belong to a
  # permitted customer"), so an owner inside such an organization can deploy too.
  invoker_iam_disabled = true
  deletion_protection  = var.deletion_protection

  template {
    service_account = google_service_account.backend.email

    scaling {
      min_instance_count = 0
      max_instance_count = var.max_instances
    }

    max_instance_request_concurrency = 40

    containers {
      image = var.backend_image

      resources {
        limits = {
          cpu    = "1"
          memory = "128Mi"
        }
        # Bill CPU only while a request is being handled (the provider default is always-on),
        # and give the instance extra CPU while it starts to shorten cold starts.
        cpu_idle          = true
        startup_cpu_boost = true
      }

      env {
        name  = "BUCKET"
        value = google_storage_bucket.photos.name
      }
      env {
        name  = "GOOGLE_CLIENT_ID"
        value = var.google_client_id
      }
      env {
        name  = "ADMIN_EMAILS"
        value = join(",", local.admin_emails)
      }
      env {
        name  = "SIGNER_SERVICE_ACCOUNT"
        value = google_service_account.backend.email
      }
      env {
        name  = "WEB_ORIGIN"
        value = local.web_origin
      }
      env {
        name  = "REQUIRE_TOKEN_BINDING"
        value = var.require_token_binding ? "true" : "false"
      }
    }
  }

  depends_on = [
    google_storage_bucket_iam_member.backend_objects,
    google_service_account_iam_member.backend_signs_as_self,
  ]
}

# Optional: hosts the Flutter Web build. Off by
# default; the owner turns it on with enable_web = true. This container only
# serves static files, so it gets its own service account with no roles at
# all rather than the project's default Compute service account (least
# privilege) - it never touches the bucket or signs a URL itself.
resource "google_service_account" "web" {
  count        = var.enable_web ? 1 : 0
  account_id   = "chamagon-web"
  display_name = "sharepi web static host"

  depends_on = [google_project_service.required]
}

resource "google_cloud_run_v2_service" "web" {
  count    = var.enable_web ? 1 : 0
  name     = "chamagon-web"
  location = var.region

  # Callable from the internet: it serves no group's data, only the static
  # app shell, so it needs no per-caller authorization (and no `allUsers`
  # binding; see the backend service above).
  invoker_iam_disabled = true
  deletion_protection  = var.deletion_protection

  template {
    service_account = google_service_account.web[0].email

    scaling {
      min_instance_count = 0
      max_instance_count = 2
    }

    containers {
      image = var.web_image

      resources {
        limits = {
          cpu    = "1"
          memory = "128Mi"
        }
        cpu_idle          = true
        startup_cpu_boost = true
      }

      # config.json (served by nginx via envsubst, docker/web.Dockerfile) tells
      # the Flutter Web build which single backend it belongs to, so it never
      # needs a manual "enter backend URL" entry (the
      # Web build is locked to the one backend it was deployed for).
      env {
        name  = "BACKEND_URL"
        value = google_cloud_run_v2_service.backend.uri
      }
    }
  }

  lifecycle {
    precondition {
      condition     = var.web_image != null
      error_message = "web_image is required when enable_web is true."
    }
  }
}

# Cloud Run writes a request log (with the caller's IP) to the project's _Default log bucket.
# The default is 30 days; the owner may lengthen it to be able to answer legal requests.
resource "google_logging_project_bucket_config" "default" {
  project        = var.project_id
  location       = "global"
  bucket_id      = "_Default"
  retention_days = var.log_retention_days
}
