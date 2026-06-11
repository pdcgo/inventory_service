# Use the official Go image
FROM golang:1.25.0 AS builder

WORKDIR /app
COPY go.mod go.sum ./

COPY . .
RUN go mod tidy
RUN go build -o server ./cmd/app_production

# Small runtime image
FROM gcr.io/distroless/base-debian12

WORKDIR /app
COPY --from=builder /app/server .

ENV PORT=8080

CMD ["/app/server"]
