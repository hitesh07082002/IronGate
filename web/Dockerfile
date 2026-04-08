FROM node:22-alpine AS build
WORKDIR /app

ARG VITE_DEMO_TOKEN=demo-token
ARG VITE_GITHUB_URL=https://github.com/hitesh07082002/IronGate
ARG VITE_API_PROXY_TARGET=http://127.0.0.1:9000

ENV VITE_DEMO_TOKEN=$VITE_DEMO_TOKEN
ENV VITE_GITHUB_URL=$VITE_GITHUB_URL
ENV VITE_API_PROXY_TARGET=$VITE_API_PROXY_TARGET

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ .
RUN npm run build

FROM nginx:1.27-alpine
COPY web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 3001
