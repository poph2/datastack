# Builds any Go service in the repo:
#   docker build --build-arg SERVICE=customer -t datastack/customer:dev .
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY pkgs ./pkgs
COPY services ./services

ARG SERVICE
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    test -n "$SERVICE" || (echo "SERVICE build arg is required" && exit 1) && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/service ./services/${SERVICE}

# distroless: no shell or package manager, runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/service /service

ENTRYPOINT ["/service"]
CMD ["serve"]
