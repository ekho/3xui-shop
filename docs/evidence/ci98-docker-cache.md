# CI #98: Docker Hub cache

Scope: [#98](https://github.com/ekho/3xui-shop/issues/98), branch
`feature/ci-dockerhub-cache` from `origin/v2` at `5bdcdbb255733052e2320d4982ec4edf10451972`.

Plan:
1. Merge the HTTPS Google cache into the existing disposable runner's daemon config,
   validate before replacement, restart before any job container, verify loaded mirrors.
2. Configure the separate BuildKit daemon and explicitly select it for image jobs,
   Compose builds and inherited native-script subprocesses; verify mirror request URLs.
3. Run source checks, independent whole-change review and real PR Platform/image CI.
   The project coordinator owns merge, post-merge publication verification and closure.

Source checks: `python3 -m unittest discover -s .github/scripts -v`, Python TOML
parser, `git diff --check`, and the existing pinned actionlint 1.7.12 command in
`docs/releases-v2.md`, applied to both changed workflows. The daemon regression
check covers preservation/idempotency, malformed settings, refusal on local or
self-hosted runners, and refusal to restart with existing containers.
The mirror-evidence pipeline also checks positive, missing-evidence and failed
producer cases with GitHub's explicit Bash/pipefail invocation.

Runtime evidence belongs to the PR's exact HEAD and Actions links. Require all
existing Platform checks and all three PR image jobs to pass; no reduced gates.
Daemon evidence is loaded `docker info` mirrors plus real canonical pulls for
BuildKit bootstrap/Compose (and QEMU after merge). It proves the configured Engine
path was exercised, not an individual cache hit. BuildKit evidence is actual
`mirror.gcr.io/v2/` HTTPS request URLs, filtered without headers or query strings.
Compose supports `BUILDX_BUILDER`; native subprocesses inherit it.
Debug logging is enabled through the builder's explicit CLI flags, preserving
the setup action's existing default entitlements. The effective container config
is read back before mirror request evidence is accepted.

Public registry preflight returned the unchanged Python 3.13, Go, PostgreSQL and
Redis pinned manifest digests on 2026-10-10; the handoff already verified pinned
Node/Caddy. Preflight does not prove runner recovery.

Limits: Google can evict cached images; a miss falls back to Docker Hub and can
still hit anonymous quotas. Product image references, versions/digests, GHCR
tagging and prerelease behavior stay intact. No local or production daemon apply.
Rollback trigger: cache setup or required CI regresses. Revert this CI diff; each
GitHub-hosted job discards its daemon at job end. Do not change a shared daemon.

Sources: [Google daemon configuration and fallback](https://docs.cloud.google.com/artifact-registry/docs/pull-cached-dockerhub-images),
[Docker BuildKit mirror and debug evidence](https://docs.docker.com/build/buildkit/configure/#registry-mirror),
[BuildKit 0.33 debug flag](https://github.com/moby/buildkit/blob/v0.33.1/cmd/buildkitd/main.go),
[Compose builder selection](https://github.com/docker/compose/blob/main/cmd/compose/build.go).
