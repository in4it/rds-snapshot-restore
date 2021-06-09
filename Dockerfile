#
# Build go project
#
FROM golang:1.15-alpine as go-builder

WORKDIR /build

COPY . .

RUN apk add -u -t build-tools curl git && \
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo && \
    apk del build-tools && \
    rm -rf /var/cache/apk/*

#
# Runtime container
#

FROM golang:alpine 
RUN mkdir /app
COPY --from=go-builder /build/rds-snapshot-restore /app
ADD entrypoint.sh /app
WORKDIR /app
RUN apk add --update curl bash ca-certificates && \
    curl -o /tmp/aws-env.tgz -L https://github.com/Droplr/aws-env/archive/v0.1.tar.gz && \
    tar -xzvf /tmp/aws-env.tgz -C /tmp && \
    mv /tmp/aws-env-0.1/bin/aws-env-linux-amd64 /bin/aws-env && \
    rm -rf /tmp/aws-env.tgz /tmp/aws-env-0.1 && \
    apk del curl && \
    rm -rf /var/cache/apk/* 
ENTRYPOINT ["/app/entrypoint.sh"]
