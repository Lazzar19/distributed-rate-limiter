FROM golang:1.27.1-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /server ./cmd/server

FROM debian:bookworm-slim

RUN useradd --system --uid 65532 --no-create-home app
USER app
COPY --from=build /server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
