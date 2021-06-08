FROM golang:alpine 
RUN mkdir /app
ADD rds-snapshot-restore /app
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