FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION/REPO let a running instance recognise its own version and ask
# GitHub whether a newer backend-vX.Y.Z tag exists (updatecheck.go). Left
# unset, the check disables itself (see version/repo's doc comment there).
ARG VERSION=dev
ARG REPO=
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.repo=${REPO}" -o /server .

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /server /server
# The distroless :nonroot image already runs as uid 65532; say so explicitly.
USER nonroot:nonroot
ENTRYPOINT ["/server"]

# The same program on AWS Lambda from an ECR image (terraform/backend-aws,
# STORAGE_PROVIDER=s3): build it with `docker build --target aws`. The adapter is a
# Lambda extension that turns Function URL events into plain HTTP requests to
# /server on $PORT. It is a separate target, not part of the default image, because
# pulling it from public.ecr.aws is rate limited for anonymous callers (HTTP 429
# "Data limit exceeded", often from GitHub's shared runners), which broke every
# build, CI and releases included, that never needed it.
FROM runtime AS aws
COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:0.9.1 /lambda-adapter /opt/extensions/lambda-adapter

# The default target is the last stage: the plain image for Cloud Run.
FROM runtime
