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

FROM gcr.io/distroless/static-debian12:nonroot
# Lets this same image run on AWS Lambda (terraform/backend-aws, STORAGE_PROVIDER=s3):
# the adapter is a Lambda extension that turns Function URL events into plain HTTP
# requests to /server on $PORT. Cloud Run (and any other container host) never
# calls it, so it does nothing there.
COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:0.9.1 /lambda-adapter /opt/extensions/lambda-adapter
COPY --from=build /server /server
# The distroless :nonroot image already runs as uid 65532; say so explicitly.
USER nonroot:nonroot
ENTRYPOINT ["/server"]
