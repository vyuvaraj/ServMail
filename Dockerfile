FROM golang:alpine AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -o servmail

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/servmail .
EXPOSE 8094
ENTRYPOINT ["./servmail"]
