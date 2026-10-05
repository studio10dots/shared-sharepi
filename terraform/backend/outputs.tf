# The URL to type into the app (Profile > Register backend).
output "backend_url" {
  description = "Enter this URL in the app."
  value       = google_cloud_run_v2_service.backend.uri
}

output "administrator" {
  description = "Google accounts that can create groups and invite people (only used when administrator_check is \"email\")."
  value       = local.admin_emails
}

output "administrator_check" {
  description = "How the backend recognises an administrator: \"user id\" (admin_subs) or \"email\" (admin_emails)."
  value       = length(var.admin_subs) > 0 ? "user id" : "email"
}

output "bucket_name" {
  value = google_storage_bucket.photos.name
}

output "web_url" {
  description = "The Web build's URL (set only when enable_web = true)."
  value       = var.enable_web ? google_cloud_run_v2_service.web[0].uri : null
}
