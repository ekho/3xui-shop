FROM node:24.11.1-alpine@sha256:682368d8253e0c3364b803956085c456a612d738bd635926d73fa24db3ce53d7 AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts
COPY . .
ARG VITE_TERMS_VERSION
ARG VITE_PRIVACY_VERSION
ARG VITE_TERMS_URL
ARG VITE_PRIVACY_URL
ARG VITE_SUPPORT_URL
RUN npm run build
FROM caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d
# High ports do not need the base image file capability; cap_drop ALL stays effective.
RUN setcap -r /usr/bin/caddy
COPY --from=build /src/dist /srv
