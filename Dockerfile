FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS build
RUN apk add --no-cache ca-certificates && mkdir /state
WORKDIR /src
COPY go.mod *.go ./
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.0.0-dev
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/fc-eddn .

FROM alpine:3.23
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /state/ /state/
COPY --from=build /out/fc-eddn /usr/local/bin/fc-eddn
USER 65532:65532
ENV FC_EDDN_STATE_DIR=/state
ENTRYPOINT ["fc-eddn"]
CMD ["sync"]
