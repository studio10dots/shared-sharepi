# English words of setup.sh's screen: the reference every other setup_msg_*.sh is
# translated from. setup_ui.sh sources them all.
#
#   ui_fmt_en KEY   a short message, a printf format (\n for a line break, %s for
#                   an argument)
#   ui_body_en KIND "what to do" for a kind of failure (see ui_classify); the text
#                   under the "What happened" heading
#
# Commands, URLs, file names and the words in angle brackets (<ACCOUNT_ID>) are
# never translated: the owner copies them. setup_test.sh checks that every
# language has every key and every kind, the same placeholders, and the same
# commands and URLs.

ui_fmt_en() {
  case "$1" in
    intro) printf '%s' "Setting up your SharePi backend. The details are written to:" ;;
    step_project) printf '%s' "Checking your project" ;;
    step_terraform) printf '%s' "Getting Terraform ready" ;;
    step_apis) printf '%s' "Turning on the Google Cloud services it uses" ;;
    step_state) printf '%s' "Preparing the place that keeps the setup state" ;;
    step_admin) printf '%s' "Finding who the administrator is" ;;
    step_init) printf '%s' "Preparing Terraform" ;;
    step_apply) printf '%s' "Creating your backend (this takes a few minutes)" ;;
    step_result) printf '%s' "Reading the result" ;;
    ok) printf '%s' "ok" ;;
    failed) printf '%s' "failed" ;;
    created) printf '%s' "created" ;;
    already_there) printf '%s' "already there" ;;
    admin_account) printf '%s' "your Google account" ;;
    admin_no_id) printf '%s' "could not read your Google user id; your email will be used" ;;
    admin_no_id_note) printf '%s' "      Run this command again later to pin it to your user id, or set admin_subs\n      (see terraform.tfvars.example)." ;;
    retrying) printf '%s' " (a service is not ready yet; trying again in 60 seconds) " ;;
    what_happened) printf '%s' "What happened" ;;
    what_to_do) printf '%s' "What to do" ;;
    last_lines) printf '%s' "The last lines of what the step printed:" ;;
    full_log) printf '%s' "Full log: %s" ;;
    final_admin) printf '%s' "Sign in to the app with the same Google account you used in this Cloud Shell:\nit is the administrator (recognised by: %s) and can create groups and invite people." ;;
    final_log) printf '%s' "The details are in this log:" ;;
    final_created) printf '%s' "Your backend is ready. In the SharePi app, go to Profile and register the URL below:" ;;
    final_unchanged) printf '%s' "Your backend is already running. In the SharePi app, go to Profile and register the URL below:" ;;
    final_updated) printf '%s' "Your backend has been updated. In the SharePi app, go to Profile and register the URL below:" ;;
  esac
}

ui_body_en() {
  local p="${SP_PROJECT:-<PROJECT_ID>}" r="${SP_REGION:-<REGION>}"
  case "$1" in
    billing) cat << EOF
  This project has no billing account linked, and Google Cloud will not create
  anything without one. Nothing has been created yet.

$(ui_t what_to_do)
  1. Open https://console.cloud.google.com/billing/linkedaccount?project=$p
  2. Click "Link a billing account" and choose your billing account.
     (No billing account yet? Create one at https://console.cloud.google.com/billing
     and link it to the project.)
  Or here, in Cloud Shell:
     gcloud billing accounts list
     gcloud billing projects link $p --billing-account=<ACCOUNT_ID>

Then run the same setup command again. It is safe to repeat.
EOF
      ;;
    protected) cat << EOF
  The setup has to replace a Cloud Run service that already exists (for example
  the one an older version created, "chamagon-backend"), and that service is
  protected against deletion.

$(ui_t what_to_do)
  1. See which service it is:
     gcloud run services list --project=$p
  2. Delete it (the older one is called chamagon-backend):
     gcloud run services delete chamagon-backend --region=$r --project=$p --quiet
     Only the service is deleted. Your photos, groups and members stay in the
     bucket.
  3. Run the same setup command again.
EOF
      ;;
    terraform) cat << EOF
  Terraform is not installed in this Cloud Shell, and it could not be fetched
  automatically.

$(ui_t what_to_do)
  Install it by following https://developer.hashicorp.com/terraform/install,
  then run the same setup command again.
EOF
      ;;
    not_ready) cat << EOF
  A Google Cloud API that was turned on a moment ago is not active everywhere yet.
  This usually clears within a few minutes.

$(ui_t what_to_do)
  Wait two or three minutes, then run the same setup command again.
EOF
      ;;
    quota) cat << EOF
  Google Cloud refused to start Cloud Run in this region for now (a per-project
  limit on how many regions a project may start using in a short time).

$(ui_t what_to_do)
  Wait a few minutes and run the same setup command again. If it keeps
  happening, try a different area code (the second argument) in a new project.
EOF
      ;;
    bucket_taken) cat << EOF
  The name for the setup-state bucket ("$p-tfstate") is already taken by
  another project. Bucket names are shared by everyone on Google Cloud.

$(ui_t what_to_do)
  Use a project whose ID has not been used for a bucket before. Do not try to
  reuse a bucket you do not own.
EOF
      ;;
    no_project) cat << EOF
  Google Cloud cannot find a project with the ID "$p", or this account cannot see it.

$(ui_t what_to_do)
  1. List the projects this account can see, and copy the ID (not the name):
     gcloud projects list
  2. Make sure Cloud Shell is signed in with the account that created the project.
  3. Run the setup command again with the right project ID.
EOF
      ;;
    permission) cat << EOF
  This account is not allowed to do that in project "$p".

$(ui_t what_to_do)
  1. Use the account that created the project (or that has the Owner role on it).
     It is shown at the top right of the Cloud Shell window.
  2. Check that the project ID is the one you meant:
     gcloud projects list
  3. Run the same setup command again.
EOF
      ;;
    network) cat << EOF
  A network request failed.

$(ui_t what_to_do)
  Wait a minute and run the same setup command again.
EOF
      ;;
    *) cat << EOF
  A step failed, and the cause is not one this script knows.

$(ui_t what_to_do)
  Run the same setup command again; many failures clear on a second try. If it
  fails the same way, send the log file named below to the person who gave you
  this command. It does not contain passwords or tokens.
EOF
      ;;
  esac
}
