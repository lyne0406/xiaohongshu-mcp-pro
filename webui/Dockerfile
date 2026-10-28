FROM golang:1.23-alpine AS build
WORKDIR /src
COPY webui/go.mod ./
COPY webui/main.go webui/index.html ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server .
FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /server /server
ENV PORT=3000
EXPOSE 3000
USER 65532:65532
ENTRYPOINT ["/server"]
