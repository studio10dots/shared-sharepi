# Maps a 2-letter area code to one balanced GCP region for that area, so the
# `terraform apply` command in docs/SETUP.md stays short: the owner types a
# 2-letter code instead of a full region id like "asia-northeast1". Source
# this file (`source region.sh`), then use `$(region_for <code>)` as the
# value of `-var="region=..."`.
#
# One region per area, chosen for low cost / good availability rather than
# lowest possible latency everywhere in that area:
region_for() {
  case "$1" in
    ea) echo asia-northeast1 ;;       # East Asia            - Tokyo
    ca) echo asia-south1 ;;           # Central / South Asia - Mumbai
    au) echo australia-southeast1 ;;  # Australia            - Sydney
    me) echo me-central1 ;;           # Middle East          - Doha
    af) echo africa-south1 ;;         # Africa               - Johannesburg
    eu) echo europe-west1 ;;          # Europe               - Belgium
    na) echo us-central1 ;;           # North America        - Iowa
    sa) echo southamerica-east1 ;;    # South America        - Sao Paulo
    *)
      echo "unknown area code \"$1\" (expected one of: ea ca au me af eu na sa)" >&2
      return 1
      ;;
  esac
}
