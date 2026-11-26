# Minimal image for proving bin/sandbox-run (bin/test-sandbox). Not a build image:
# it holds a shell and busybox, nothing a real step needs.
#   docker build -t dots-sandbox-test deploy/sandbox
#   DOTS_TEST_IMAGE=dots-sandbox-test bin/test-sandbox
FROM alpine:3.20
RUN mkdir /work
WORKDIR /work
