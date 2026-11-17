FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
RUN apk add --no-cache ca-certificates && mkdir /state
WORKDIR /src
COPY go.mod *.go ./
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.0.0-dev
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/fc-eddn .

FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /state/ /state/
COPY --from=build /out/fc-eddn /usr/local/bin/fc-eddn
USER 65532:65532
ENV FC_EDDN_STATE_DIR=/state
ENTRYPOINT ["fc-eddn"]
CMD ["sync"]
