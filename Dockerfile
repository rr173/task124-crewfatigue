# builder: identical Go toolchain to the local machine
FROM docker.m.daocloud.io/library/golang:1.26.3-bookworm AS builder
WORKDIR /src
# Resolve deps from go.mod/go.sum (vendor/ is excluded from context by .dockerignore).
COPY go.mod go.sum ./
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=sum.golang.google.cn
RUN go mod download
COPY . .
RUN go build -o /out/task124-crewfatigue .

# runtime: minimal image
FROM docker.m.daocloud.io/library/alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /out/task124-crewfatigue /task124-crewfatigue
ENTRYPOINT ["/task124-crewfatigue"]
CMD ["--smoke-test"]
