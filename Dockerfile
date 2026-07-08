# ---- build stage ----
FROM golang:1.23-alpine AS build
WORKDIR /src

# Cache dependencies first.
COPY go.mod ./
COPY go.sum* ./
RUN go mod download

# Build a fully static, CGO-free binary (pure-Go SQLite + embedded frontend).
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/server /server

ENV DB_PATH=/data/mealplanner.db \
    PORT=8080
EXPOSE 8080
VOLUME ["/data"]

ENTRYPOINT ["/server"]
