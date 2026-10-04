# Set once by the app's publisher, before the repository is shared. Terraform
# loads *.auto.tfvars by itself, so an owner passes only the project and the
# region on the command line (docs/SETUP.md, SetupGuidePage) and edits nothing.
# Both values are public: the image is pulled by Cloud Run, and the client ID
# is the only audience the backend accepts ID tokens for.
backend_image    = "REPLACE_WITH_PUBLISHED_IMAGE" # e.g. ghcr.io/<publisher>/chamagon-backend:1
google_client_id = "REPLACE_WITH_PUBLISHERS_WEB_CLIENT_ID.apps.googleusercontent.com"
