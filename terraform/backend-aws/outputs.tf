# The URL to type into the app (Profile > Register backend).
output "backend_url" {
  description = "Enter this URL in the app."
  value       = trimsuffix(aws_lambda_function_url.backend.function_url, "/")
}

output "administrator" {
  description = "Google accounts that can create groups and invite people."
  value       = local.admin_emails
}

output "bucket_name" {
  value = aws_s3_bucket.photos.bucket
}
