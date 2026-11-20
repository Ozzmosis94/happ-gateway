FROM golang:1.26.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /happ-gateway ./cmd/happ-gateway
RUN mkdir -p /runtime-data && chown 65532:65532 /runtime-data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /happ-gateway /happ-gateway
COPY --from=build --chown=65532:65532 /runtime-data /data
WORKDIR /data
ENV DATA_DIR=/data WEB_ADDR=127.0.0.1:8088
VOLUME ["/data"]
ENTRYPOINT ["/happ-gateway"]
CMD ["server"]
