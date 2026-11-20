FROM golang:1.26.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /raygate ./cmd/raygate

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /raygate /raygate
WORKDIR /data
ENV RAYGATE_HOME=/data RAYGATE_WEB_ADDR=127.0.0.1:8088
VOLUME ["/data"]
ENTRYPOINT ["/raygate"]
CMD ["server"]
