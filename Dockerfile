FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bqrest ./cmd/bqrest

FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.source="https://github.com/sgoley/bqrest"
COPY --from=build /out/bqrest /bqrest
EXPOSE 8080
ENTRYPOINT ["/bqrest"]
