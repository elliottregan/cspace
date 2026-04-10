---
title: Installation
description: How to install cspace on your machine.
sidebar:
  order: 1
---

Install cspace globally using the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/elliottregan/cspace/main/install.sh | bash
source ~/.zshrc  # or restart your shell
```

## Prerequisites

- **Docker** with Docker Compose v2
- **jq** for JSON processing
- **gum** (optional) for interactive TUI — `brew install gum`
- **gh** (optional) for GitHub integration — `brew install gh`
- **`GH_TOKEN`** in your project `.env` — required for git push/pull from inside the container
