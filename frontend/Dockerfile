# Next.js standalone server (SSR theming template butuh cookies() sehingga
# static export tidak memungkinkan; standalone = server Node minimal)
FROM node:22-alpine AS build
RUN corepack enable
WORKDIR /app
COPY package.json pnpm-lock.yaml .npmrc ./
RUN pnpm install --frozen-lockfile
COPY . .
ENV NEXT_PUBLIC_API_URL=""
RUN pnpm build

FROM node:22-alpine
RUN adduser -D -u 10001 app
USER app
WORKDIR /app
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
COPY --from=build /app/public ./public
ENV NODE_ENV=production HOSTNAME=0.0.0.0 PORT=3000
EXPOSE 3000
CMD ["node", "server.js"]
