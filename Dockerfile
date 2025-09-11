FROM golang:1.24 as build
WORKDIR /app
# Expect the repository root as build context when building this image
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /usr/local/bin/slinky ./

FROM alpine:3.20
RUN adduser -D -u 10001 appuser \
    && apk add --no-cache curl jq ca-certificates
COPY --from=build /usr/local/bin/slinky /usr/local/bin/slinky
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
USER appuser
ENTRYPOINT ["/entrypoint.sh"]


