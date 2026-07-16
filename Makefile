.PHONY: test test-go test-shell coverage build-interpose build-pulse fmt fmt-check

test: test-go test-shell

test-go:
	go test -race -coverprofile=coverage.out ./...

test-shell:
	@if command -v bats >/dev/null 2>&1; then \
		bats git/tests/ installer/tests/; \
	else \
		echo "bats not installed; skipping shell tests"; \
	fi

coverage: test-go
	go tool cover -func=coverage.out

build-interpose:
	go build -o bin/interpose ./interpose

build-pulse:
	go build -o bin/pulse ./pulse

fmt:
	./sandbox/format-c.sh

fmt-check:
	./sandbox/format-c.sh --dry-run
