# Hugging Face package release flow

This project publishes Hugging Face Spaces in package mode to avoid rebuilding
the frontend and Go backend inside the Space builder.

## One-time setup

The local `space` remote should point to the target Space:

```bash
git remote set-url space https://Vick888888@huggingface.co/spaces/Vick888888/VickLuckeyMe
```

## Publish a new package

Push your code to `myfork/main`. The `HF Package` GitHub Actions workflow builds
and uploads this fixed release asset:

```text
https://github.com/wanggang8/sub2api/releases/download/hf-latest/sub2api-hf-linux-amd64.tar.gz
```

You can also run the workflow manually from GitHub Actions when you want to
refresh the package without another code push.

## Publish the Space

After the GitHub Action has uploaded the package, publish the Space snapshot:

```bash
HF_PACKAGE_URL="https://github.com/wanggang8/sub2api/releases/download/hf-latest/sub2api-hf-linux-amd64.tar.gz" \
bash deploy/publish-hf-space.sh
```

In package mode, the Space repo receives only a minimal `README.md` and
`Dockerfile`. The Dockerfile downloads the packaged runtime during the Space
build, so the Space builder does not run `pnpm install`, frontend build, or
`go build`.

## Local package build

For local verification, create the same tarball shape with:

```bash
bash deploy/build-hf-package.sh
```

The package is written under `release/hf/`.

## Notes

- The Space commit hash differs from `main` because `publish-hf-space.sh`
  creates a clean one-commit snapshot.
- Do not commit the generated `release/hf/*.tar.gz` file.
- If the package URL changes, pass the new URL through `HF_PACKAGE_URL`.
