# CI #98: Docker Hub cache

Scope: [#98](https://github.com/ekho/3xui-shop/issues/98), branch
`feature/ci-dockerhub-cache` from `origin/v2` at `5bdcdbb255733052e2320d4982ec4edf10451972`.

Plan:
1. Isolate client auth in a fresh job-scoped `DOCKER_CONFIG`; merge the HTTPS Google
   cache into the existing disposable runner's daemon config,
   validate before replacement, restart before any job container, verify loaded mirrors.
2. Configure the separate BuildKit daemon and explicitly select it for image jobs,
   Compose builds and inherited native-script subprocesses; verify mirror HTTP trace spans.
3. Run source checks, independent whole-change review and real PR Platform/image CI.
   The project coordinator owns merge, post-merge publication verification and closure.

Source checks: `python3 -m unittest discover -s .github/scripts -v`, Python TOML
parser, `git diff --check`, and the existing pinned actionlint 1.7.12 command in
`docs/releases.md`, applied to both changed workflows. The daemon regression
check covers preservation/idempotency, malformed settings, refusal on local or
self-hosted runners, and refusal to restart with existing containers.
The trace verifier covers exact HTTPS host/port/method/status, missing evidence,
warm-record traversal, invalid config/identifiers, failed producers and private output.

Runtime evidence belongs to the PR's exact HEAD and Actions links. Require all
existing Platform checks and all three PR image jobs to pass; no reduced gates.
Daemon evidence is loaded `docker info` mirrors plus real canonical pulls for
BuildKit bootstrap/Compose (and QEMU after merge). It proves the configured Engine
path was exercised, not an individual cache hit. BuildKit evidence requires a
successful HTTPS manifest HEAD to `mirror.gcr.io` in a completed build's HTTP trace.
The verifier prints only a count and keeps its raw JSON/URL/header data in memory.
Compose supports `BUILDX_BUILDER`; native subprocesses inherit it.
Debug logging is enabled through the builder's explicit CLI flags, preserving
the setup action's existing default entitlements. The effective container config
is read back before mirror request evidence is accepted.

BuildKit 0.33.1's private tracing logger takes precedence over containerd's later
logger context, dropping URL/host fields while retaining direct request fields.
The [focused runner diagnostic](https://github.com/ekho/3xui-shop/actions/runs/37999410317)
confirmed that exact distinction. Required route evidence therefore uses Buildx's
existing history trace, whose HTTP attributes retain the actual URL and host.
When stdout is a pipe, the command emits JSON without a UI server or browser.
Completed records are searched because a later warm build may make no HTTP request.
Missing evidence or command failure still fails the job; the verifier writes no raw trace.

The hosted runner's default client config contained a Docker Hub auth key.
On two runners, default canonical Redis pulls succeeded through fallback while
their journal windows reported mirror manifest `unauthorized`; the same pulls
with an empty client config produced no fallback. Anonymous mirror token and
manifest requests returned 200. Docker reuses supplied Hub credentials for mirror
authorization. The isolated job config removes that inherited auth and retains
the later GHCR login in the same directory. Original client settings are untouched.
A repeated digest pull can reuse its local manifest; the comparison alone does
not prove recovery. Acceptance requires the full final HEAD on fresh job runners.
Diagnostic evidence: [PR runner](https://github.com/ekho/3xui-shop/actions/runs/37997221147),
[push runner](https://github.com/ekho/3xui-shop/actions/runs/37997215927).

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
[BuildKit logger precedence](https://github.com/moby/buildkit/blob/v0.33.1/util/bklog/log.go#L33-L40),
[Buildx headless history trace](https://github.com/docker/buildx/blob/2d379c0c3f22da0d2759d132a0ec81ca949098f0/commands/history/trace.go#L140-L167),
[HTTP trace attributes](https://github.com/open-telemetry/opentelemetry-go-contrib/blob/c8a87a60ba1b3374fd16df11fc3eeae6c41abbc9/instrumentation/net/http/otelhttp/internal/semconv/client.go#L53-L126),
[Compose builder selection](https://github.com/docker/compose/blob/main/cmd/compose/build.go),
[Docker client config isolation](https://docs.docker.com/reference/cli/docker/#change-the-docker-directory),
[Docker mirror credential store](https://github.com/moby/moby/blob/6430e49a55babd9b8f4d08e70ecb2b68900770fe/registry/auth.go#L36-L51).
