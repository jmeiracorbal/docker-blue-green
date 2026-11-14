FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod main.go version.txt ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/e2e .

FROM scratch
COPY --from=builder --chown=65532:65532 /out/e2e /e2e
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/e2e"]
CMD ["serve"]
