ARG app=bad-proxy
ARG project=github.com/cjimti/bad-proxy
ARG buildsrc=./cmd/server/main.go

FROM golang:1.24.2-alpine3.21 AS builder

ARG app
ARG project
ARG buildsrc
ARG version

ENV PROJECT=${project} \
    BUILDSRC=${buildsrc} \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

RUN mkdir -p /go/src/ \
 && mkdir -p /go/bin \
 && mkdir -p /go/pkg

ENV PATH=/go/bin:$PATH

RUN mkdir -p /go/src/$PROJECT/
ADD . /go/src/$PROJECT/

WORKDIR /go/src/$PROJECT/

RUN go build -ldflags "-X main.Version=${version} -extldflags \"-static\"" -o /go/bin/app $BUILDSRC

RUN echo "nobody:x:65534:65534:Nobody:/:" > /etc_passwd

FROM scratch

ENV PATH=/bin

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc_passwd /etc/passwd
COPY --from=builder /go/bin/app /bin/bad-proxy

WORKDIR /

USER nobody
ENTRYPOINT ["/bin/bad-proxy"]