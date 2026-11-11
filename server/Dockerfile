# Build: docker build -t ner0bit-server server/
FROM golang:1.24-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY ui ./ui
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ner0bit-server . \
 && mkdir -p /out/data

# Image chạy: chỉ có binary tĩnh, không shell; có CA certs để gọi HTTPS tới Vrain/METAR. Giờ VN tính bằng offset cố định nên không cần tzdata.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/ner0bit-server /ner0bit-server
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENV NER0BIT_ADDR=:8080 \
    NER0BIT_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/ner0bit-server"]
