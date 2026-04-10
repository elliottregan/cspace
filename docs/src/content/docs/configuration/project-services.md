---
title: Project Services
description: Adding project-specific Docker Compose services like databases and backends to cspace instances.
sidebar:
  order: 2
---

import { Aside } from '@astrojs/starlight/components';

cspace instances run in Docker Compose stacks. You can extend the stack with project-specific services — databases, caches, message queues, or any other container your project needs — by providing an additional Docker Compose file.

## Adding services

### 1. Create a Docker Compose file

Place a `docker-compose.yml` in the `.cspace/` directory at your project root:

```yaml title=".cspace/docker-compose.yml"
services:
  # Extend the devcontainer with project-specific env vars
  devcontainer:
    environment:
      - DATABASE_URL=postgresql://dev:dev@postgres:5432/myapp

  postgres:
    image: postgres:16
    container_name: ${CSPACE_PREFIX}.${COMPOSE_PROJECT_NAME}.postgres
    environment:
      POSTGRES_DB: myapp
      POSTGRES_USER: dev
      POSTGRES_PASSWORD: dev
    volumes:
      - postgres-data:/var/lib/postgresql/data
    networks:
      - default

volumes:
  postgres-data:
```

### 2. Reference it in `.cspace.json`

```json title=".cspace.json"
{
  "services": ".cspace/docker-compose.yml"
}
```

<Aside type="caution">
The `services` path is relative to the project root. Make sure the file exists at the specified location before running `cspace up`.
</Aside>

## How it works

When `cspace up` launches an instance, it composes multiple Docker Compose files together:

1. **Core compose** — the built-in devcontainer definition
2. **Shared services** — browser sidecars (Playwright, Chromium CDP)
3. **Project services** — your `.cspace/docker-compose.yml` (if configured)

Docker Compose merges these files, so your services file can both add new services and extend the existing `devcontainer` service with additional environment variables, volumes, or other settings.

## Compose interpolation variables

Use these Docker Compose interpolation variables to keep container names unique across instances:

| Variable | Description | Example value |
|----------|-------------|---------------|
| `${CSPACE_PREFIX}` | The project's short prefix from config | `mp` |
| `${COMPOSE_PROJECT_NAME}` | The full compose project name (prefix + instance name) | `mp-mars` |

<Aside type="tip">
Always use these variables in `container_name` to avoid naming collisions when running multiple instances of the same project.
</Aside>

## Post-setup hook

For initialization that needs to run after the container is created — such as database migrations, seed data, or one-time setup — use a post-setup script.

### 1. Create the script

```bash title=".cspace/post-setup.sh"
#!/bin/bash
# Example: set up a database
set -euo pipefail

if [ -f /workspace/.cspace-db-done ]; then exit 0; fi

echo "Running database migrations..."
cd /workspace && npm run migrate

touch /workspace/.cspace-db-done
```

### 2. Reference it in `.cspace.json`

```json title=".cspace.json"
{
  "post_setup": ".cspace/post-setup.sh"
}
```

<Aside>
Make your post-setup script **idempotent** — it may run more than once if a container is recreated. The sentinel file pattern (`if [ -f /workspace/.cspace-db-done ]; then exit 0; fi`) shown above is one way to ensure the script only runs once.
</Aside>

## Example: full services setup

Here's a complete example combining services and post-setup for a Node.js project with PostgreSQL:

```json title=".cspace.json"
{
  "project": {
    "name": "my-app"
  },
  "container": {
    "ports": {
      "3000": "dev server"
    }
  },
  "verify": {
    "all": "npm run lint && npm run typecheck && npm run test",
    "e2e": "npm run e2e"
  },
  "services": ".cspace/docker-compose.yml",
  "post_setup": ".cspace/post-setup.sh"
}
```

```yaml title=".cspace/docker-compose.yml"
services:
  devcontainer:
    environment:
      - DATABASE_URL=postgresql://dev:dev@postgres:5432/myapp

  postgres:
    image: postgres:16
    container_name: ${CSPACE_PREFIX}.${COMPOSE_PROJECT_NAME}.postgres
    environment:
      POSTGRES_DB: myapp
      POSTGRES_USER: dev
      POSTGRES_PASSWORD: dev
    volumes:
      - postgres-data:/var/lib/postgresql/data
    networks:
      - default

volumes:
  postgres-data:
```

```bash title=".cspace/post-setup.sh"
#!/bin/bash
set -euo pipefail
if [ -f /workspace/.cspace-db-done ]; then exit 0; fi
cd /workspace && npm run migrate
touch /workspace/.cspace-db-done
```

Each cspace instance gets its own Postgres container with its own data volume, so agents working in parallel never collide.
