FROM --platform=$BUILDPLATFORM node:24.11.1-alpine@sha256:682368d8253e0c3364b803956085c456a612d738bd635926d73fa24db3ce53d7 AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts
COPY . .
RUN npm run build
FROM caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d
# High ports do not need the base image file capability; cap_drop ALL stays effective.
RUN setcap -r /usr/bin/caddy && apk add --no-cache jq=1.8.2-r0
COPY --from=build /src/dist /srv
COPY --chmod=0755 scripts/runtime-config.sh /usr/local/bin/runtime-config
ENTRYPOINT ["/usr/local/bin/runtime-config"]
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
