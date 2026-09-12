FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/anydoc-go ./third_party/anydoc-go
RUN go mod download
COPY plugin/proto ./plugin/proto
COPY plugin/sdk/transport ./plugin/sdk/transport
COPY examples/plugins/mock-model-provider ./examples/plugins/mock-model-provider
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/plugin ./examples/plugins/mock-model-provider

FROM scratch
COPY --from=build /out/plugin /plugin
ENTRYPOINT ["/plugin"]
