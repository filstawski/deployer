# Preparing a project for Deployer

Deployer builds one Docker image from the repository and runs it as one HTTP
service. A project needs:

1. a **Dockerfile** that builds the app and starts it;
2. an app that **listens on `0.0.0.0`** on a known port inside the container;
3. a **health check path** that answers with a status below 400 once the app can serve requests;
4. a **`deployer.yaml`** at the repository root.

All of it must be committed and pushed; the server never sees local files.

## deployer.yaml

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/filstawski/deployer/main/schemas/deployer.schema.json
version: 1
source:
  dockerfile: Dockerfile   # relative to the repository root
  context: .               # build context, relative to the repository root
http:
  port: 3000               # the port the app listens on inside the container
  healthcheck:
    path: /health          # checked until it answers with a status below 400
    timeout: 90s           # from 1s to 5m
environment:               # optional plain variables; never secrets
  NODE_ENV: production
```

| Field | Default | Notes |
| --- | --- | --- |
| `version` | required | Always `1`. |
| `source.dockerfile` | required | Must exist inside the repository. |
| `source.context` | `.` | Can differ from the Dockerfile's directory, for monorepos. |
| `http.port` | required | Must match what the app actually listens on. |
| `http.healthcheck.path` | `/health` | Use `/` for static sites. |
| `http.healthcheck.timeout` | `90s` | Raise it for apps that start slowly, up to `5m`. |
| `environment` | none | At most 100 names. `PORT` and `DEPLOYER_*` are reserved. |
| `http.service` | `web` | Reserved; leave it out. |
| `project` | none | Informational; ignored. |

Unknown fields are errors. Check the file with `deployer validate --json`.

Once the Dockerfile exists, `deployer init --json` writes a starting file. It
takes the port from the Dockerfile's `EXPOSE` line unless `--port` is given, and
uses `/health` unless `--health` is given: `deployer init --port 3000 --health / --json`.

## What the app gets at runtime

| Variable | Example |
| --- | --- |
| `PORT` | `3000`, the value of `http.port` |
| `DEPLOYER_URL` | `https://design-a.previews.example.com` |
| `DEPLOYER_REF` | `design/a` |
| `DEPLOYER_COMMIT` | the full commit SHA |
| `DEPLOYER_DEPLOYMENT_ID` | `dep_k7m2q9x4abcd` |

These exist only when the container runs, not during `docker build`. A frontend
that bakes its public URL in at build time can't use `DEPLOYER_URL`; read the URL
at runtime, or use relative URLs.

Requests arrive through a proxy with the original `Host` header, plus
`X-Forwarded-Proto: https`, `X-Forwarded-Host`, and `X-Forwarded-For`.
Frameworks that check allowed hosts, build absolute URLs, or set secure cookies
must trust these headers and allow the preview domain.

## Limits to design for

- **No outbound network access at runtime**, unless the server enables egress.
  The app can't call external APIs, payment providers, or hosted databases. Use
  sample data, mocks, or an embedded database such as SQLite. Builds do have
  internet access, for installing dependencies.
- **One container.** There is no separate database service yet. Files written
  inside the container survive stop and start, but are deleted with the
  deployment.
- **Resources:** 1 CPU, 512 MiB of memory, and 256 processes per preview by
  default. Builds get 2 CPUs and 2 GiB, and time out after 20 minutes.
- **Security:** no privileged mode, and reduced Linux capabilities. Running as a
  non-root user is fine.
- **Not fetched:** Git submodules, Git LFS files, and the `.git` directory.
- **Reserved paths:** everything under `/_deployer/` is answered by Deployer
  itself.

## Templates

Pick the closest one and adapt the commands to the project's package manager
and scripts. Always add a `.dockerignore`.

### Node.js server (Express, Fastify, Hono, ...)

The app must listen on `process.env.PORT` and host `0.0.0.0`, for example
`app.listen(process.env.PORT || 3000, "0.0.0.0")`.

```dockerfile
FROM node:22-alpine
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build --if-present
ENV NODE_ENV=production
EXPOSE 3000
CMD ["npm", "start"]
```

```yaml
version: 1
source:
  dockerfile: Dockerfile
http:
  port: 3000
  healthcheck:
    path: /health      # or / if there is no health route
```

`npm ci` needs a committed `package-lock.json`. For pnpm, add `RUN corepack enable`
and use `pnpm install --frozen-lockfile`. For Yarn, use `yarn install --immutable`.

### Next.js

```dockerfile
FROM node:22-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /app ./
EXPOSE 3000
CMD ["npx", "next", "start", "-H", "0.0.0.0", "-p", "3000"]
```

```yaml
version: 1
source:
  dockerfile: Dockerfile
http:
  port: 3000
  healthcheck:
    path: /
    timeout: 2m
```

`NEXT_PUBLIC_*` values are compiled in at build time. Large builds may need more
than the build container's 2 GiB; if a build is killed, ask an administrator.

### Static site built with Vite, React, Vue, Astro, ...

Build the site, then serve the output with nginx. The `try_files` line makes
client-side routes work after a reload.

```dockerfile
FROM node:22-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /app/dist /usr/share/nginx/html
RUN printf 'server {\n  listen 80;\n  root /usr/share/nginx/html;\n  location / { try_files $uri $uri/ /index.html; }\n}\n' > /etc/nginx/conf.d/default.conf
EXPOSE 80
```

```yaml
version: 1
source:
  dockerfile: Dockerfile
http:
  port: 80
  healthcheck:
    path: /
```

Change `/app/dist` to the framework's output directory if it differs, for example
`build` for Create React App, or `out` for a static Next.js export.

### Plain HTML without a build step

```dockerfile
FROM nginx:1.27-alpine
COPY . /usr/share/nginx/html
EXPOSE 80
```

Use `port: 80` and `path: /`. Keep `.git`, `Dockerfile`, and `deployer.yaml` out
with `.dockerignore`.

### Python (FastAPI, Flask, Django)

```dockerfile
FROM python:3.13-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
EXPOSE 8000
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
```

- **Flask:** `CMD ["gunicorn", "--bind", "0.0.0.0:8000", "app:app"]`, with `gunicorn`
  in `requirements.txt`. The Flask development server needs `--host 0.0.0.0`.
- **Django:** `CMD ["gunicorn", "--bind", "0.0.0.0:8000", "mysite.wsgi"]`, with
  `gunicorn` in `requirements.txt`. Also allow
  the preview hosts and trust the proxy, for example by setting
  `ALLOWED_HOSTS = [".previews.example.com"]`,
  `CSRF_TRUSTED_ORIGINS = ["https://*.previews.example.com"]`, and
  `SECURE_PROXY_SSL_HEADER = ("HTTP_X_FORWARDED_PROTO", "https")`. Run migrations
  against SQLite in the start command if the app needs a database.

Use `port: 8000`, and a health path the app serves.

### Go

```dockerfile
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /app .

FROM scratch
COPY --from=build /app /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app"]
```

The program should listen on `":" + os.Getenv("PORT")`. Add the CA bundle
(`COPY --from=build /etc/ssl/certs /etc/ssl/certs`) only if it makes HTTPS calls.

### Monorepo

Point the Dockerfile and the context at the right places. With `context: .`, the
Dockerfile can copy shared packages from the repository root:

```yaml
version: 1
source:
  dockerfile: apps/web/Dockerfile
  context: .
http:
  port: 3000
```

## .dockerignore

```text
.git
node_modules
dist
build
.next
.env*
*.log
```

## Checklist before deploying

- The Dockerfile builds: `docker build .`, when Docker is available locally.
- The app listens on `0.0.0.0` and on the port in `deployer.yaml`.
- The health path answers quickly without depending on external services.
- The app starts without outbound internet access and without secrets.
- `deployer validate --json` succeeds.
- Everything is committed and pushed.
