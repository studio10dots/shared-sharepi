# Set once by the app's publisher, before the repository is shared. Terraform
# loads *.auto.tfvars by itself, so an owner passes only the project and the
# region on the command line (docs/SETUP.md) and edits nothing.
# Public values: the image is pulled by Cloud Run. The Web client ID is not set
# here any more: Terraform reads it from config/<environment>.json, the one
# place it is kept (Agents.md section 8).
backend_image = "ghcr.io/studio10dots/chamagon-backend:0.1.5" # a fixed version, raised by hand after each release
