# syntax=docker/dockerfile:1
# Static web client: Vite build in a node stage, served by an unprivileged nginx (non-root, port 8080).
# Build context is the repository root; web.Dockerfile.dockerignore keeps it to web/ and docker/.
FROM node:24-bookworm-slim AS build
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.29-alpine
COPY docker/nginx.conf /etc/nginx/conf.d/default.conf
COPY docker/security-headers.conf /etc/nginx/snippets/security-headers.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 8080
