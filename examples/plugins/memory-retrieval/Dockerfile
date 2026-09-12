FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /out/plugin ./examples/plugins/memory-retrieval

FROM scratch
COPY --from=build /out/plugin /plugin
ENTRYPOINT ["/plugin"]
