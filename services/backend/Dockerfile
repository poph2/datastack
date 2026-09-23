FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/backend .

FROM alpine:3.20
RUN adduser -D -H app
COPY --from=build /out/backend /usr/local/bin/backend
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/backend"]
