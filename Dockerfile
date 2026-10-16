FROM node:24-alpine AS ui
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server ./cmd/server
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ffmpeg fonts-noto-cjk && rm -rf /var/lib/apt/lists/* && useradd -u 10001 -m app
WORKDIR /app
COPY --from=build /server /app/server
COPY --from=ui /src/frontend/dist /app/frontend/dist
RUN mkdir /app/data && chown app /app/data
USER app
EXPOSE 8080
ENTRYPOINT ["/app/server"]
