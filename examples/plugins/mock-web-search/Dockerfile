FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY plugin/proto ./plugin/proto
COPY plugin/sdk/transport ./plugin/sdk/transport
COPY examples/plugins/mock-web-search ./examples/plugins/mock-web-search
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/plugin ./examples/plugins/mock-web-search

FROM scratch
COPY --from=build /out/plugin /plugin
ENTRYPOINT ["/plugin"]
