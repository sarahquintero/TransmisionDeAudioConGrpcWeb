# Etapa 1: compilación del frontend con Webpack
FROM node:24-alpine AS build

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY webpack.config.js index.html funciones.js ./
COPY proto ./proto
RUN npx webpack --mode production

# Etapa 2: servidor estático Nginx
FROM nginx:alpine

COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
