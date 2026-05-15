---
title: Contribute to Docs
linkTitle: Docs
weight: 15
description: Build and preview this site locally.
---

The site lives in
[`site/`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/tree/main/site)
and is built with [Hugo](https://gohugo.io/) (the extended build) and the
[Docsy](https://www.docsy.dev/) theme.

## Quick start

You need [Hugo (extended)](https://gohugo.io/installation/),
[Go](https://go.dev/doc/install), [Node.js](https://nodejs.org/) (which
provides `npm`), and [Git](https://git-scm.com/downloads) installed. The versions on your machine should meed the minium required versions listed in
[`netlify.toml`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/main/netlify.toml).

```bash
git clone https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu.git
cd dra-driver-nvidia-gpu/site
npm ci          # one-time; installs PostCSS deps from package-lock.json
npm run serve   # http://localhost:1313, live-reload, drafts visible
```

To produce the same output Netlify builds for production, from `site/`:

```bash
npm run build   # output written to ./public
```

## Where to look for more details

| If you need to know… | Look at |
| --- | --- |
| Exact Hugo / Go / Node versions used for production builds | [`netlify.toml`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/main/netlify.toml) |
| Minimum Hugo version enforced locally, plus all site configuration | [`site/hugo.toml`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/main/site/hugo.toml) |
| Docsy theme version this site pins | [`site/go.mod`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/main/site/go.mod) |
| npm scripts and PostCSS dependencies | [`site/package.json`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/main/site/package.json) |
| How to install Hugo on any OS (use the extended build) | [Hugo installation docs](https://gohugo.io/installation/) |
| Docsy theme prerequisites, configuration, and authoring guide | [Docsy documentation](https://www.docsy.dev/docs/) |
