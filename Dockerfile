# ---- web build ----
FROM node:24-alpine AS webbuild
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- go build ----
FROM golang:1.24-alpine AS gobuild
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -o /forge-space-server ./cmd/forge-space-server \
 && CGO_ENABLED=0 go build -o /forge-space-mcp ./cmd/forge-space-mcp

# ---- runtime ----
FROM alpine:3.21
WORKDIR /app
COPY --from=gobuild /forge-space-server /forge-space-mcp /usr/local/bin/
COPY --from=webbuild /web/dist /app/web/dist
EXPOSE 8080
CMD ["forge-space-server"]
