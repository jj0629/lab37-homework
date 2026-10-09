# One image, three binaries: server (api/worker roles), mock-api, mock-webhook.
# Build context is the repo root so the sample data can be bundled.
FROM golang:1.24-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS="-mod=vendor -trimpath"
COPY backend/ ./
# Dependencies are vendored: the build needs no network access.
RUN go build -ldflags="-s -w" -o /out/server ./cmd/server \
 && go build -ldflags="-s -w" -o /out/mock-api ./cmd/mock-api \
 && go build -ldflags="-s -w" -o /out/mock-webhook ./cmd/mock-webhook

FROM scratch
COPY --from=build /out/ /app/
# --chmod: readable by the non-root user even if the files are owner-only on disk
COPY --chmod=0755 sample-data/ /app/sample-data/
ENV SAMPLE_DIR=/app/sample-data
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/app/server"]
