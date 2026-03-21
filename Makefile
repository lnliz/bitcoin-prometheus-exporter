.PHONY: test coverage coverage-clean

COVERPKG ?= ./...
UNIT_COVER ?= coverage.unit.out
INT_COVER ?= coverage.integration.out
COVER_OUT ?= coverage.out
COVER_HTML ?= coverage.html

test:
	go test ./...

coverage-clean:
	rm -f $(UNIT_COVER) $(INT_COVER) $(COVER_OUT) $(COVER_HTML)

coverage: coverage-clean
	go test -covermode=atomic -coverpkg=$(COVERPKG) -coverprofile=$(UNIT_COVER) ./...
	cp $(UNIT_COVER) $(COVER_OUT)
	@if [ "$(REAL_RPC_TESTS)" = "1" ]; then \
		REAL_RPC_TESTS=1 \
		REAL_RPC_HOST="$(REAL_RPC_HOST)" \
		REAL_RPC_USER="$(REAL_RPC_USER)" \
		REAL_RPC_PASSWORD="$(REAL_RPC_PASSWORD)" \
		REAL_RPC_SCHEME="$(REAL_RPC_SCHEME)" \
		go test -run Integration -covermode=atomic -coverpkg=$(COVERPKG) -coverprofile=$(INT_COVER) ./...; \
		awk 'FNR==1 && NR!=1 {next} {print}' $(UNIT_COVER) $(INT_COVER) > $(COVER_OUT); \
	fi
	go tool cover -func=$(COVER_OUT) | grep "^total:"
	go tool cover -html=$(COVER_OUT) -o $(COVER_HTML)
