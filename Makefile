.PHONY: test test-go test-shell coverage build-interpose

test: test-go test-shell

test-go:
	go test -race -coverprofile=coverage.out ./...

test-shell:
	@if command -v bats >/dev/null 2>&1; then \
		bats git/tests/; \
	else \
		echo "bats not installed; skipping shell tests"; \
	fi

coverage: test-go
	go tool cover -func=coverage.out

build-interpose:
	go build -o bin/interpose ./interpose
