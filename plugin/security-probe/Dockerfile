FROM golang:1.26-alpine AS build
WORKDIR /src
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /probe main.go

FROM scratch
COPY --from=build /probe /probe
ENTRYPOINT ["/probe"]
