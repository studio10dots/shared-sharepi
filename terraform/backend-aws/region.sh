# AWS counterpart of terraform/backend/region.sh: the same 2-letter area
# codes, mapped to one balanced AWS region per area instead of a GCP one.
# Source this file (`source region.sh`), then use `$(region_for <code>)` as
# the value of `-var="region=..."`.
region_for() {
  case "$1" in
    ea) echo ap-northeast-1 ;;  # East Asia            - Tokyo
    ca) echo ap-south-1 ;;      # Central / South Asia - Mumbai
    au) echo ap-southeast-2 ;;  # Australia            - Sydney
    me) echo me-central-1 ;;    # Middle East          - UAE
    af) echo af-south-1 ;;      # Africa               - Cape Town
    eu) echo eu-west-1 ;;       # Europe               - Ireland
    na) echo us-east-1 ;;       # North America        - N. Virginia
    sa) echo sa-east-1 ;;       # South America        - Sao Paulo
    *)
      echo "unknown area code \"$1\" (expected one of: ea ca au me af eu na sa)" >&2
      return 1
      ;;
  esac
}
