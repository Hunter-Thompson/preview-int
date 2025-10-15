FROM node:lts-alpine AS build

WORKDIR /app

COPY package*.json ./

RUN npm install

COPY . .

ARG APP_BUILD_ID="dev"
ARG APP_DEPLOYMENT_ID="local"

ENV VITE_APP_BUILD_ID=${APP_BUILD_ID}
ENV VITE_APP_DEPLOYMENT_ID=${APP_DEPLOYMENT_ID}

RUN npm run build

FROM nginx:1.29-alpine

COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
