#!/usr/bin/env bash
# Sourced by setup.sh. Buckets used to be named "<project_id>-tfstate" and
# "<project_id>-photos"; they are now "sharepi-<project_id>-tfstate" and
# "sharepi-<project_id>-photos" (the prefix says whose they are in the console and
# keeps a project ID that starts with "goog" from making an invalid name). An
# owner who set up under the earlier names is moved over by running the same
# setup command again: nothing for them to do, nothing deleted.
#
# Functions only; they expect project_id and SP_LOG to be set by setup.sh.

# True when the bucket belongs to this project. A listing is per project, so a
# bucket of the same name in somebody else's project does not count as ours. The
# answer is read into a variable first: grep -q ending the pipe early would make
# gcloud fail with SIGPIPE under pipefail.
bucket_in_project() {
  local names
  names="$(gcloud storage buckets list --project="$project_id" --format='value(name)' 2>> "$SP_LOG" || true)"
  grep -qx -- "$1" <<< "$names"
}

# True when the object exists. Used to tell a bucket that is ours (it holds what
# SharePi's setup or backend wrote) from one that merely has the earlier name:
# only the first is copied.
object_exists() {
  gcloud storage ls "gs://${1}/${2}" > /dev/null 2>&1
}

# copy_bucket <from> <to> [extra gcloud arguments]: every object of <from> into
# <to>, names and metadata unchanged (who uploaded a photo and when it was taken
# live in the metadata, and the key the roster is hashed with is an object too:
# a bucket without them is a different backend). rsync, so a run that was cut
# short carries on where it stopped. Nothing in <from> is touched.
copy_bucket() {
  local from="$1" to="$2"
  shift 2
  gcloud storage rsync --recursive "$@" "gs://${from}" "gs://${to}"
}

# The name of the bucket that the Terraform state holds for the photos, or
# nothing when the state does not hold one (yet).
tracked_photos_bucket() {
  terraform state show -no-color google_storage_bucket.photos 2>> "$SP_LOG" \
    | sed -n 's/^[[:space:]]*name[[:space:]]*=[[:space:]]*"\(.*\)"[[:space:]]*$/\1/p' \
    | head -n 1
}
