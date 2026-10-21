FROM golang:1.27.3-alpine AS build
WORKDIR /src
RUN apk add --no-cache make ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 make build

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S app -G app
USER app
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --retries=15 CMD wget -q -O /dev/null http://127.0.0.1:8080/health || exit 1

FROM runtime AS user-service
COPY --from=build /src/bin/user-service /usr/local/bin/user-service
ENTRYPOINT ["user-service"]

FROM runtime AS commerce-service
COPY --from=build /src/bin/commerce-service /usr/local/bin/commerce-service
ENTRYPOINT ["commerce-service"]

FROM runtime AS gateway
COPY --from=build /src/bin/gateway /usr/local/bin/gateway
ENTRYPOINT ["gateway"]
