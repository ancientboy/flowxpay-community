FROM golang:1.26.11-alpine AS api-build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/cmd/api ./cmd/api
COPY backend/internal ./internal
COPY backend/migrations ./migrations
RUN CGO_ENABLED=0 go build -trimpath -o /out/flowxpay-api ./cmd/api

FROM alpine:3.23 AS api
COPY --from=api-build /out/flowxpay-api /usr/local/bin/flowxpay-api
COPY --chmod=0555 deploy/compose/api-entrypoint.sh /usr/local/bin/flowxpay-entrypoint
USER 10001:10001
ENTRYPOINT ["/bin/sh", "/usr/local/bin/flowxpay-entrypoint"]

FROM node:22-alpine AS web-build
WORKDIR /src
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci
COPY web/index.html web/tsconfig.json web/vite.config.mjs ./
COPY web/src ./src
RUN npm run build

FROM nginx:alpine AS web
RUN rm /etc/nginx/conf.d/default.conf
COPY deploy/compose/nginx.conf /etc/nginx/conf.d/flowxpay.conf
COPY --from=web-build /src/dist/client /usr/share/nginx/html
