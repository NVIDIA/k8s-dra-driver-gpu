# Contributing to the Documentation

The documentation site is built with [MkDocs Material](https://squidfunk.github.io/mkdocs-material/) and hosted on [Netlify](https://www.netlify.com/). All source files live in the [`docs/`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/tree/main/docs) directory. The site configuration is in [`mkdocs.yml`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/main/mkdocs.yml) at the root of the repository.

## Site structure

| Path | Purpose |
|---|---|
| `mkdocs.yml` | Site config, theme, nav, plugins |
| `docs/` | Markdown source files |
| `requirements.txt` | Python dependencies for building the site |
| `netlify.toml` | Netlify build settings |

Navigation is defined explicitly in `mkdocs.yml` under `nav:`. Adding a new page requires both a new `.md` file in `docs/` and an entry in the nav.

## Set up a local preview

### Prerequisites

- Python 3.11 or later
- `pip`

### Install dependencies

```sh
pip install -r requirements.txt
```

### Start the dev server

```sh
mkdocs serve
```

The site is served at `http://127.0.0.1:8000/`. The server watches `docs/` and `mkdocs.yml` and reloads on any change.

To bind to a different address or port:

```sh
mkdocs serve --dev-addr=127.0.0.1:8001
```

### Build the site

```sh
mkdocs build
```

Output is written to `site/`. This directory is listed in `.gitignore` and should not be committed.

## Making changes

1. Edit or add Markdown files under `docs/`.
2. If adding a new page, add it to the `nav:` section of `mkdocs.yml`.
3. Run `mkdocs serve` and verify your changes in the browser.
4. Open a pull request. The Netlify bot will post a deploy preview URL on the PR.

## Netlify build settings

The site builds automatically on every push to `main` and on every pull request (as a deploy preview). Build settings are defined in `netlify.toml`:

- **Build command:** `mkdocs build`
- **Publish directory:** `site`
- **Python version:** 3.11

If you add a new MkDocs plugin or Python dependency, add it to `requirements.txt` so Netlify picks it up.
