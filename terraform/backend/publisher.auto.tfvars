# Set once by the app's publisher, before the repository is shared. Terraform
# loads *.auto.tfvars by itself, so an owner passes only the project and the
# region on the command line (docs/SETUP.md) and edits nothing.
# Both values are public: the image is pulled by Cloud Run, and the client ID
# is the only audience the backend accepts ID tokens for.
backend_image    = "ghcr.io/studio10dots/chamagon-backend:0.1.0" # a fixed version, raised by hand after each release
google_client_id = "1070200079236-cvpbopkvsqekvv5esbiusubltdoq65o9.apps.googleusercontent.com"
