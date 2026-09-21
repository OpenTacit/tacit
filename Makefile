# Tacit — build and test entry points.
#
# The one trap this file exists to prevent: a deployed binary built without
# -tags onnx silently reverts retrieval to the lexical hashing embedder
# (docs/design/embedder-onnx.md). Plain `make build` / `make test` are for
# development; anything headed for a real registry goes through
# `make install` or `make deploy`, which always carry the tag.
#
# Two build tags, and they fail in opposite directions. Leave out `onnx` and
# retrieval degrades in silence. Leave out `pg` and a postgres:// URL is
# refused loudly, by name, at startup — so the default build can drop the
# driver (cmd/tacit/storebackend.go) and stay honestly dependency-light. Every
# production target below carries both.

GO      ?= go
PKG     ?= ./...
BIN     := $(HOME)/go/bin/tacit
INGRESS := $(HOME)/go/bin/tacit-ingress

# Stamped into `tacit version`. Releases pass the tag; dev builds get the
# nearest tag + commit (or the bare commit before any tag exists).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: all build build-onnx install install-ingress ingress dist-ingress image deploy site plugins prices test test-go test-plugins test-pg licenses licenses-report vet fmt clean

all: build test

## build: compile everything, no tags (dev loop; hashing-v1 retrieval, no Postgres)
build:
	$(GO) build $(LDFLAGS) $(PKG)

## build-onnx: compile with the semantic embedder and Postgres — what production runs
build-onnx:
	$(GO) build -tags onnx,pg $(LDFLAGS) $(PKG)

## install: install the production binary (both tags mandatory)
install:
	$(GO) install -tags onnx,pg $(LDFLAGS) ./cmd/tacit

## install-ingress: install the ingress binary.
## No ONNX tag: the ingress embeds and retrieves nothing. It routes and stores
## only its own records plus opted-in aggregate import outcomes.
install-ingress:
	$(GO) install $(LDFLAGS) ./cmd/tacit-ingress

## dist-ingress: the deployable ingress binary, in dist/. Static (CGO off), so
## it runs on any distribution regardless of glibc vintage — the ingress needs
## no cgo at all, unlike the registry with its ONNX embedder. Override ARCH for
## an arm64 host: make dist-ingress ARCH=arm64
ARCH ?= amd64
dist-ingress:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) $(GO) build -trimpath $(LDFLAGS) \
		-o dist/tacit-ingress-linux-$(ARCH) ./cmd/tacit-ingress
	@echo "built dist/tacit-ingress-linux-$(ARCH) — copy to /usr/local/bin/tacit-ingress on the host"

## ingress: run a local ingress for testing — status page on http://localhost:8443,
## tunnel on :8444, data under ./ingress-data, no sign-in. The route table is
## `tacit-ingress instances list`; the operator console is not in this
## repository (pkg/ingress/operator.go says where it went and why).
ingress: install-ingress
	$(INGRESS) serve

## image: build the container image locally (docs/distribution/docker-plan.md).
## Mirrors what ci/release do: the linux binary into dist/, then buildx
## assembles it onto distroless. Linux-only (CGO can't cross-compile from
## macOS); releases publish multi-arch from the tag workflow.
image:
	mkdir -p dist
	CGO_ENABLED=1 $(GO) build -tags onnx,pg -trimpath $(LDFLAGS) -o dist/tacit-linux-amd64 ./cmd/tacit
	docker buildx build --platform linux/amd64 -t tacit:local --load .

## deploy: install (with the ONNX tag — see `install` above), restart the
## registry service, retire the on-demand processes still running the binary
## this install just replaced, and CHECK THAT THE SEMANTIC EMBEDDER IS SERVING.
##
## The tag has always been on the build. What was missing is the other half:
## proof that it reached the running service. A binary without it does not fail
## — retrieval silently falls back to the lexical hashing embedder, every stored
## vector is in the wrong space, and the first sign is that suggestions stop
## matching. That is the trap this file's header names, and until now `deploy`
## only checked that the registry answered at all.
##
## Thirty seconds to answer, not ten. A cold start on the semantic embedder
## loads the runtime and embeds whatever arrived since the last one, which took
## thirteen seconds the day the pin moved — a deploy that worked, reported as a
## registry that never came up.
##
## So the health endpoint decides. `embed_degraded` is the registry's own
## admission that it wanted one embedder and is serving another, which is
## exactly what a build missing the tag looks like from outside, and it is a
## hard failure here. A registry deliberately configured for lexical retrieval
## is not degraded and is not failed — it is reported, because a deploy should
## say which retrieval it just put in service.
DEPLOY_BASE ?= http://127.0.0.1:8080
DEPLOY_HEALTH ?= $(DEPLOY_BASE)/v1/health
deploy: install retire-stale
	systemctl --user restart tacit-registry.service
	@health=''; \
	for i in $$(seq 30); do \
		sleep 1; health=$$(curl -sf --max-time 3 $(DEPLOY_HEALTH)) && break; \
	done; \
	if [ -z "$$health" ]; then \
		echo "registry NOT healthy after deploy" >&2; \
		code=$$(curl -s -o /tmp/tacit-deploy-health.$$$$ -w '%{http_code}' --max-time 3 $(DEPLOY_HEALTH) 2>/dev/null); \
		if [ -z "$$code" ] || [ "$$code" = "000" ]; then \
			echo "  nothing is answering $(DEPLOY_HEALTH)" >&2; \
		else \
			echo "  $(DEPLOY_HEALTH) answered HTTP $$code: $$(head -c 200 /tmp/tacit-deploy-health.$$$$)" >&2; \
			echo "  503 here means the registry is in first-run setup — it found no registry.env and no key" >&2; \
			echo "  in the environment, so it parked behind /setup rather than serving with a known default." >&2; \
		fi; \
		rm -f /tmp/tacit-deploy-health.$$$$; \
		echo "  the service log has the rest:  journalctl --user -u tacit-registry -n 30" >&2; \
		exit 1; fi; \
	echo "registry healthy"; \
	case "$$health" in \
	  *'"embed_degraded":true'*) \
	    echo "retrieval DEGRADED: this registry wants $$(printf '%s' "$$health" | sed -n 's/.*\"embed_wanted\":\"\([^\"]*\)\".*/\1/p') and is serving $$(printf '%s' "$$health" | sed -n 's/.*\"embed_model\":\"\([^\"]*\)\".*/\1/p')." >&2; \
	    echo "a binary built without -tags onnx does this, and so does a missing model file." >&2; \
	    exit 1 ;; \
	esac; \
	echo "retrieval: $$(printf '%s' "$$health" | sed -n 's/.*\"embed_model\":\"\([^\"]*\)\".*/\1/p')"
	@# The dashboard reads a SEALED blob, so a deploy alone leaves it showing
	@# whatever the previous binary published — for up to ten minutes, during
	@# which a new field is missing from the page and missing looks like broken.
	@# One forced publish makes the page show what was just built. A machine with
	@# no registry key has no ledger and says so quietly: not a failed deploy.
	@# Publish to THIS registry directly rather than through its public address.
	@# The configured registry URL is the ingress, so a deploy-time publish would
	@# leave the machine, cross the internet and come back to the process that has
	@# just restarted — which is precisely when the ingress has not reconnected.
	@# It timed out once for exactly that reason, which is a deploy reporting a
	@# failure that has nothing to do with the deploy.
	@bin=$$($(GO) env GOBIN); [ -n "$$bin" ] || bin=$$($(GO) env GOPATH)/bin; \
	if out=$$(TACIT_REGISTRY_URL=$(DEPLOY_BASE) "$$bin/tacit" usage --publish 2>&1); then \
	  echo "usage ledger: $$out"; \
	else \
	  echo "usage ledger: not published — $$out"; \
	fi
	@url=$$(sed -n 's/^TACIT_EXTERNAL_URL=//p' "$$HOME/.config/tacit/registry.env" 2>/dev/null); \
	echo "review it: $${url:-http://127.0.0.1:8080}/usage"


## site: render the project page and its assets into dist/site, for any static
## host to serve at a zone's apex. The bundle is generated from internal/ui and
## never committed — a test holds index.html byte-identical to what the ingress
## renders, so there is one page and two ways to serve it.
##
## Uploading it is somebody's deployment, not this project's business: the
## bundle is a directory, and where it goes depends on who is hosting.
SITE_DIR ?= dist/site
site:
	$(GO) run ./hack/sitegen -o $(SITE_DIR)

## retire-stale: kill `tacit mcp` / `tacit serve-hooks` processes still running a
## REPLACED binary, so the install that just happened actually takes effect.
##
## These two are spawned on demand — by the harness for MCP, by a hook relay for
## the agent — and nothing ever restarts them. An MCP server outlives the session
## that started it and can run for weeks: a fix that reserved `shown` for what a
## member actually sees shipped on 2026-07-24 and was still writing inflated
## funnel events the next day, because the process serving tacit_search had been
## up since the 14th. A fix that never reaches the running process is not shipped.
##
## Only a process whose /proc/PID/exe is marked (deleted) is killed — that is
## exactly "started from a binary no longer on disk", which is what `go install`
## leaves behind. The exe check also keeps a shell whose command line merely
## MENTIONS these commands out of it, which a pgrep pattern alone would catch.
## The registry service is excluded: systemd owns it, and the line above restarts
## it. Linux-only; on a host with no /proc the readlink fails and nothing is
## killed, so a macOS operator restarts their harness to pick up a new binary.
retire-stale:
	@for pid in $$(pgrep -f 'tacit (mcp|serve-hooks)' 2>/dev/null); do \
	  case "$$(readlink /proc/$$pid/exe 2>/dev/null)" in \
	    */tacit\ \(deleted\)) \
	      echo "retiring stale $$(tr '\0' ' ' < /proc/$$pid/cmdline 2>/dev/null) (pid $$pid)"; \
	      kill $$pid 2>/dev/null || true ;; \
	  esac; \
	done; true

## plugins: regenerate every per-harness skill/command file from plugins/_src.
## The generated files ARE the committed source of truth; never hand-edit them
## (cmd/tacit-genplugins/pluginsgen_test.go fails CI if you do). Edit _src instead.
plugins:
	$(GO) run ./cmd/tacit-genplugins

## prices: refresh internal/pricing/published.json from the genai-prices
## catalogue. The written file IS the committed source of truth — nothing
## fetches a price at run time, so re-run this when rates move and commit the
## diff (internal/pricing/published.go explains what the conversion keeps).
prices:
	$(GO) run ./cmd/tacit-genprices

## test: the full local suite — vet + hermetic Go tests + plugin source tests
test: vet test-go test-plugins

## test-go: the Go suite, under the race detector — see ci.yml for why it is
## not optional. TACIT_NO_RACE=1 skips it for a quick local loop.
test-go:
	$(GO) test $(if $(TACIT_NO_RACE),,-race) $(PKG)

## test-plugins: node source-level tests for the TypeScript harness adapters
test-plugins:
	node --test plugins/amp/test/tacit-plugin.test.mjs plugins/pi/test/tacit-extension.test.mjs plugins/opencode/test/tacit-plugin.test.mjs

## test-pg: the Postgres conformance suite against a real database.
## Skipped-by-default is the trap (docs/dev/09-testing.md): run this before
## merging any storage change. Starts a throwaway container if none is up.
PG_URL := postgres://postgres:pg@127.0.0.1:15432/postgres?sslmode=disable
test-pg:
	@docker ps --format '{{.Names}}' | grep -q '^tacit-pg-test$$' || \
		docker run -d --name tacit-pg-test -e POSTGRES_PASSWORD=pg -p 15432:5432 postgres:16-alpine
	@until docker exec tacit-pg-test pg_isready -U postgres -q 2>/dev/null; do sleep 0.3; done
	TACIT_TEST_DB_URL="$(PG_URL)" $(GO) test -tags pg -count=1 ./internal/registry/pgstore/ ./internal/registry/storage/...

## licenses: fail if a dependency arrives under a license the project has not
## accepted. Scans both the default and the released build — a module reachable
## only under -tags pg still ships in every release binary. Needs the network
## once, to fetch the pinned scanner.
licenses:
	$(GO) run ./hack/licensecheck

## licenses-report: regenerate the dependency table in THIRD-PARTY-NOTICES.md.
## Run it after any go.mod change and paste the result over the old table.
licenses-report:
	$(GO) run ./hack/licensecheck -report

vet:
	$(GO) vet $(PKG)

fmt:
	gofmt -w $$($(GO) list -f '{{.Dir}}' $(PKG))

clean:
	-docker rm -f tacit-pg-test 2>/dev/null || true
