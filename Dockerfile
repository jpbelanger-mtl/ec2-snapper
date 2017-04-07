FROM alpine:3.5

RUN set -ex \
 && apk add --no-cache ca-certificates

ADD ./build/ec2-snapper-linux-amd64.tgz /

ENTRYPOINT ["/ec2-snapper/ec2-snapper"]
