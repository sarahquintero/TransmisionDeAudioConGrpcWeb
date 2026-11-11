# Etapa 1: compilación del frontend con Webpack
FROM node:24-alpine AS build

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY webpack.config.js index.html funciones.js ./
COPY proto ./proto
RUN npx webpack --mode production

# Etapa 2: servidor estático Nginx (sin caché para index.html)
FROM nginx:alpine

COPY --from=build /app/dist /usr/share/nginx/html
RUN printf 'server {\n  listen 80;\n  root /usr/share/nginx/html;\n  index index.html;\n  location = / {\n    add_header Cache-Control "no-store, no-cache, must-revalidate";\n    try_files /index.html =404;\n  }\n  location = /index.html {\n    add_header Cache-Control "no-store, no-cache, must-revalidate";\n  }\n  location / {\n    try_files $uri $uri/ =404;\n  }\n}\n' > /etc/nginx/conf.d/default.conf
EXPOSE 80
