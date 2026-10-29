# `make ci` runs the every-push checks. Tool versions are pinned here and
# installed into bin/; nothing here becomes a module dependency.

GOLANGCI_LINT_VERSION := v2.14.0
GOIMPORTS_VERSION     := v0.52.0
GOVULNCHECK_VERSION   := v1.9.0
BIN                   := $(abspath bin)
PKGS                  := ./...
FUZZTIME              ?= 30s

.PHONY: all ci tools verify build fmt vet lint fsm selfcheck test test-386 qemu race fuzz seeded cover vuln

all: ci

ci: verify build fmt vet lint fsm selfcheck race test-386 fuzz seeded cover vuln

# ---- toolchain ------------------------------------------------------------

tools: $(BIN)/golangci-lint $(BIN)/goimports $(BIN)/govulncheck

$(BIN)/golangci-lint:
	GOBIN=$(BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(BIN)/goimports:
	GOBIN=$(BIN) go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

$(BIN)/govulncheck:
	GOBIN=$(BIN) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

# The module stays stdlib-only: no require lines, a tidy go.mod, and no
# tracked binaries.
verify:
	go mod verify
	go mod tidy -diff
	@! grep -q '^require' go.mod || { echo "go.mod must have no require lines"; exit 1; }
	@bad="$$(git ls-files -z | xargs -0 file --mime-type | grep -E 'application/(x-executable|x-pie-executable|x-sharedlib|x-mach-binary|x-dosexec|wasm|x-object|x-archive)' | cut -d: -f1)"; \
	  if [ -n "$$bad" ]; then echo "tracked binary files:"; echo "$$bad"; exit 1; fi

build:
	go build -trimpath $(PKGS)

# ---- formatting, vet, lint ------------------------------------------------

fmt: $(BIN)/goimports
	@out="$$(gofmt -l . ; $(BIN)/goimports -l .)"; if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

# No build constraints and no go:generate.
vet:
	go vet $(PKGS)
	@! grep -rn --include='*.go' --exclude-dir=bin --exclude-dir=.git --exclude-dir=testdata -E '^//go:(build|generate)' . || { echo "build constraints and go:generate are not allowed"; exit 1; }

lint: $(BIN)/golangci-lint
	$(BIN)/golangci-lint run $(PKGS)

# merilo's FSM rules over this module's machines and holders. merilovet
# reads the files one target builds, so it runs once per supported target;
# code that only builds elsewhere would otherwise go unchecked.
FSM_TARGETS := linux/amd64 linux/386 windows/amd64 darwin/arm64 js/wasm wasip1/wasm

fsm:
	@go build -o $(BIN)/merilovet ./cmd/merilovet
	@for t in $(FSM_TARGETS); do \
	  echo "merilovet $$t"; \
	  GOOS=$${t%/*} GOARCH=$${t#*/} $(BIN)/merilovet || exit 1; \
	done

# The code safety self-check on merilo and merilovet, and its fixture.
selfcheck:
	go test -count=1 -run 'TestSelfCheck' .

# ---- tests ------------------------------------------------------------------

test:
	go test -count=1 $(PKGS)

# int and uint are 32 bits here, so a counter that only fits in 64 bits,
# or a conversion that only narrows on 32-bit targets, fails here first.
test-386:
	GOARCH=386 go vet $(PKGS)
	GOARCH=386 go test -count=1 $(PKGS)

# The whole suite on other CPUs under qemu-user: 32- and 64-bit, big- and
# little-endian. Not part of ci, because it needs qemu-user installed. Each
# entry is GOARCH:qemu-binary; override QEMU_TARGETS to run a subset.
QEMU_TARGETS := arm:qemu-arm arm64:qemu-aarch64 loong64:qemu-loongarch64 \
                mips:qemu-mips mipsle:qemu-mipsel mips64:qemu-mips64 mips64le:qemu-mips64el \
                ppc64:qemu-ppc64 ppc64le:qemu-ppc64le riscv64:qemu-riscv64 s390x:qemu-s390x

qemu:
	@for t in $(QEMU_TARGETS); do \
	  arch=$${t%%:*}; emu=$${t#*:}; \
	  command -v $$emu >/dev/null || { echo "$$emu not found; install qemu-user"; exit 1; }; \
	  echo "go test GOARCH=$$arch under $$emu"; \
	  GOARCH=$$arch GOARM=7 go test -count=1 -exec $$emu $(PKGS) || exit 1; \
	done

race:
	go test -race -count=1 -timeout 30m $(PKGS)

# Every fuzz target except FuzzSeededShallow, which is meant to fail (seeded).
fuzz:
	@for f in $$(grep -rl --include='*_test.go' '^func Fuzz' . | xargs -r dirname | sort -u); do \
	  for name in $$(grep -ho '^func Fuzz[A-Za-z0-9_]*' $$f/*_test.go | sed 's/^func //' | grep -v '^FuzzSeededShallow$$'); do \
	    go test -run='^$$' -fuzz="^$$name$$" -fuzztime=$(FUZZTIME) $$f || exit 1; \
	  done; \
	done

# Fuzzing seeded by a bounded search must find the seeded bug
# beyond the bound. The failing input Go writes is removed afterwards.
seeded:
	@cd examples/twophase/coord && \
	  if go test -run='^$$' -fuzz='^FuzzSeededShallow$$' -fuzztime=$(FUZZTIME) . >/dev/null 2>&1; then \
	    rm -rf testdata/fuzz; echo "seeded failed: fuzzing did not find the seeded bug"; exit 1; \
	  else rm -rf testdata/fuzz; echo "seeded ok: fuzzing found the seeded bug"; fi

cover:
	@mkdir -p $(BIN)
	@go test -count=1 -coverprofile=$(BIN)/merilo.cov . >/dev/null && \
	  echo "merilo coverage $$(go tool cover -func=$(BIN)/merilo.cov | tail -1 | grep -o '[0-9.]*%')"

vuln: $(BIN)/govulncheck
	$(BIN)/govulncheck $(PKGS)
