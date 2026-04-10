---
title: Git Authentication
description: Configure git authentication for cspace devcontainers.
sidebar:
  order: 3
---

Containers have no access to your host's SSH agent, so all git operations go over HTTPS using a GitHub personal access token. cspace wires this up automatically — you just need to provide the token.
