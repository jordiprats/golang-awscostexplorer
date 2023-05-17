FROM golang:1.18.3 as builder

WORKDIR /build

ADD main.go /build
ADD go.sum /build
ADD go.mod /build
ADD Makefile /build

RUN make all

# container
FROM alpine:latest

COPY --from=builder /build/awscost /usr/local/bin

WORKDIR /web

ADD public /web

ENV GIN_MODE=release

ENTRYPOINT [ "/usr/local/bin/awscost" ]