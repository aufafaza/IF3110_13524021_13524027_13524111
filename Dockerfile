# syntax=docker/dockerfile:1

FROM golang:1.27.1-alpine AS backend-build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed-admin ./cmd/seed-admin

FROM node:22-alpine AS frontend-build

WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM alpine:3.22 AS backend

RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=backend-build /out/server /app/server
COPY --from=backend-build /out/seed-admin /app/seed-admin
RUN mkdir -p /app/var/log/migration-platform && chown -R app:app /app
USER app
EXPOSE 8080
CMD ["/app/server"]

FROM nginx:1.29-alpine AS frontend

COPY --from=frontend-build /src/frontend/dist /usr/share/nginx/html
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
