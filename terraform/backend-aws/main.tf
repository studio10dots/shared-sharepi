data "aws_caller_identity" "me" {}

locals {
  bucket_name   = coalesce(var.bucket_name, "chamagon-photos-${data.aws_caller_identity.me.account_id}")
  admin_emails  = [for e in var.admin_emails : lower(e)]
  function_name = "chamagon-backend"
  use_binary    = var.backend_binary != null

  # The Web OAuth client ID has one home: config/<environment>.json at the
  # repository root, read here exactly as terraform/backend does, so the app a
  # build signs in with and the audience this backend accepts cannot drift apart.
  google_client_id = coalesce(
    var.google_client_id,
    jsondecode(file("${path.module}/../../config/${var.environment}.json")).GOOGLE_SIGN_IN_CLIENT_ID,
  )
}

# One bucket for all groups. Only the backend's execution role can reach it
# (Agents.md section 9): there is no per-group IAM here, the backend itself
# authorizes every request against {group_id}/members.json.
resource "aws_s3_bucket" "photos" {
  bucket = local.bucket_name
}

resource "aws_s3_bucket_public_access_block" "photos" {
  bucket = aws_s3_bucket.photos.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "photos" {
  bucket = aws_s3_bucket.photos.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Versioning is what stands in for GCS's generation numbers and soft delete on
# this side: a conditional write matches the current version's ETag, and a
# "deleted" object is a delete marker over still-present noncurrent versions.
resource "aws_s3_bucket_versioning" "photos" {
  bucket = aws_s3_bucket.photos.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "photos" {
  bucket     = aws_s3_bucket.photos.id
  depends_on = [aws_s3_bucket_versioning.photos]

  # Cost-optimisation equivalent of the GCP module's Autoclass: objects move
  # between access-frequency tiers automatically instead of by a fixed age.
  rule {
    id     = "intelligent-tiering"
    status = "Enabled"
    filter {}
    transition {
      days          = 0
      storage_class = "INTELLIGENT_TIERING"
    }
  }

  # Soft-delete window: once a version is no longer current (the live object
  # was overwritten or deleted), it is purged after this many days, and an
  # old delete marker with nothing behind it is cleared out too.
  rule {
    id     = "soft-delete-expiry"
    status = "Enabled"
    filter {}
    noncurrent_version_expiration {
      noncurrent_days = var.soft_delete_retention_days
    }
    expiration {
      expired_object_delete_marker = true
    }
  }

  # Share-link manifests (Agents.md section 3, "{group_id}/shares/{share_id}.share.json")
  # are access grants that already expire in minutes via their own signed URL;
  # this just clears the small objects themselves out of the bucket afterwards.
  # S3 lifecycle rules cannot filter by suffix (unlike the GCS module's
  # matches_suffix), only by prefix or tag, and group_id varies per group, so
  # the S3 store implementation must tag each share manifest it writes with
  # chamagon-expire=share for this rule to find it.
  rule {
    id     = "share-manifest-expiry"
    status = "Enabled"
    filter {
      tag {
        key   = "chamagon-expire"
        value = "share"
      }
    }
    expiration {
      days = 1
    }
    noncurrent_version_expiration {
      noncurrent_days = 1
    }
  }

  # Interrupted large-video uploads never get named parts cleaned up otherwise.
  rule {
    id     = "abort-incomplete-multipart"
    status = "Enabled"
    filter {}
    abort_incomplete_multipart_upload {
      days_after_initiation = 1
    }
  }
}

data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "backend" {
  name               = "chamagon-backend"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

# Least privilege: object-level access on this bucket only, nothing project-
# (account-) wide (Agents.md section 13). GetObjectVersion/ListBucketVersions
# and DeleteObjectVersion back Store.ListDeleted/Restore/ReadDeleted; the two
# *Tagging actions are only used when writing a share manifest (see the
# share-manifest-expiry lifecycle rule above).
data "aws_iam_policy_document" "backend_bucket_access" {
  statement {
    actions = [
      "s3:GetObject",
      "s3:GetObjectVersion",
      "s3:PutObject",
      "s3:PutObjectTagging",
      "s3:GetObjectTagging",
      "s3:DeleteObject",
      "s3:DeleteObjectVersion",
    ]
    resources = ["${aws_s3_bucket.photos.arn}/*"]
  }
  statement {
    actions = [
      "s3:ListBucket",
      "s3:ListBucketVersions",
    ]
    resources = [aws_s3_bucket.photos.arn]
  }
}

resource "aws_iam_role_policy" "backend_bucket_access" {
  name   = "bucket-access"
  role   = aws_iam_role.backend.id
  policy = data.aws_iam_policy_document.backend_bucket_access.json
}

resource "aws_iam_role_policy_attachment" "backend_logs" {
  role       = aws_iam_role.backend.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

# Created explicitly (rather than left for Lambda to create on first
# invocation) so retention_in_days applies from the start; Lambda's own log
# group naming convention (/aws/lambda/<function name>) is relied on here.
resource "aws_cloudwatch_log_group" "backend" {
  name              = "/aws/lambda/${local.function_name}"
  retention_in_days = var.log_retention_days
}

# The program as a zip, for the no-ECR route. The file inside is named
# "bootstrap" and is executable (output_file_mode applies to the zip's entries).
data "archive_file" "backend" {
  count            = local.use_binary ? 1 : 0
  type             = "zip"
  source_file      = var.backend_binary
  output_path      = "${path.module}/.backend.zip"
  output_file_mode = "0755"
}

resource "aws_lambda_function" "backend" {
  function_name = local.function_name
  role          = aws_iam_role.backend.arn
  memory_size   = var.memory_size
  timeout       = var.timeout_seconds

  # Either the container image (ECR) or the program itself, never both.
  package_type = local.use_binary ? "Zip" : "Image"
  image_uri    = local.use_binary ? null : var.backend_image

  filename         = local.use_binary ? data.archive_file.backend[0].output_path : null
  source_code_hash = local.use_binary ? data.archive_file.backend[0].output_base64sha256 : null
  # provided.al2023 runs the file named by the handler. With the adapter's
  # AWS_LAMBDA_EXEC_WRAPPER the adapter starts it and forwards requests to $PORT.
  runtime       = local.use_binary ? "provided.al2023" : null
  handler       = local.use_binary ? "bootstrap" : null
  architectures = ["x86_64"]
  layers = local.use_binary ? [
    "arn:aws:lambda:${var.region}:753240598075:layer:LambdaAdapterLayerX86:${var.web_adapter_layer_version}",
  ] : []

  # -1 is "no reservation" (see max_concurrency).
  reserved_concurrent_executions = var.max_concurrency == null ? -1 : var.max_concurrency

  environment {
    variables = merge(
      {
        # The program is the same one Cloud Run runs: this picks the S3 store and
        # signer (backend/s3.go). Region and credentials come from Lambda itself
        # (AWS_REGION and the execution role); there is no SIGNER_SERVICE_ACCOUNT.
        STORAGE_PROVIDER = "s3"
        BUCKET           = aws_s3_bucket.photos.bucket
        GOOGLE_CLIENT_ID = local.google_client_id
        ADMIN_EMAILS     = join(",", local.admin_emails)
        ADMIN_SUBS       = join(",", var.admin_subs)
        # Browser (Web build) access is not set up on AWS: empty refuses all of it.
        WEB_ORIGIN            = ""
        REQUIRE_TOKEN_BINDING = var.require_token_binding ? "true" : "false"
        # The Lambda Web Adapter (baked into the image, backend/Dockerfile, or the
        # layer for backend_binary) reads this to know which port the program
        # listens on.
        PORT = "8080"
      },
      # With the layer, the adapter is started through Lambda's exec wrapper.
      local.use_binary ? { AWS_LAMBDA_EXEC_WRAPPER = "/opt/bootstrap" } : {},
    )
  }

  # Exactly one way to deliver the program. (A variable's validation cannot see
  # another variable in Terraform 1.7, so it is checked here.)
  lifecycle {
    precondition {
      condition     = (var.backend_binary == null) != (var.backend_image == null)
      error_message = "Set exactly one of backend_image (an ECR image) and backend_binary (a built program)."
    }
    # On AWS the owner is always named explicitly: there is nothing to infer it
    # from, and a backend with no administrator could never create a group.
    precondition {
      condition     = length(var.admin_subs) > 0 || length(local.admin_emails) > 0
      error_message = "Name the owner: set admin_subs (the Google user id the app's setup guide puts in the command) or admin_emails."
    }
  }

  depends_on = [
    aws_iam_role_policy.backend_bucket_access,
    aws_iam_role_policy_attachment.backend_logs,
    aws_cloudwatch_log_group.backend,
  ]
}

# Callable from the internet: the backend authenticates every request itself
# (Google ID token), exactly as the GCP module's Cloud Run service is
# invokable by allUsers.
resource "aws_lambda_function_url" "backend" {
  function_name      = aws_lambda_function.backend.function_name
  authorization_type = "NONE"
}

# Auth type NONE alone is not enough: until the function's resource-based policy
# allows the public in, every request gets "403 AccessDeniedException" from AWS
# before it reaches the backend. The console and SAM add this policy by
# themselves; Terraform, the CLI and the API do not. Since October 2025 a new
# function URL needs both statements: one to use the URL, one to invoke the
# function (only through the URL, so the function cannot be invoked any other
# way by the public).
resource "aws_lambda_permission" "function_url_public" {
  statement_id           = "FunctionURLAllowPublicAccess"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.backend.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}

resource "aws_lambda_permission" "function_url_invoke" {
  statement_id             = "FunctionURLInvokeAllowPublicAccess"
  action                   = "lambda:InvokeFunction"
  function_name            = aws_lambda_function.backend.function_name
  principal                = "*"
  invoked_via_function_url = true
}
