# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM alpine:3.22
RUN apk add --no-cache openvpn iproute2 ca-certificates
COPY --from=build /out/vpngate-proxy /out/vpngate-feeder /usr/local/bin/
COPY config.example.yaml /etc/vpngate-proxy/config.yaml
COPY profiles/examples /profiles/examples
ENTRYPOINT ["vpngate-proxy"]
CMD ["serve", "-config", "/etc/vpngate-proxy/config.yaml"]
