BIN := dotular
CMD := ./cmd/dotular

.PHONY: build run tidy clean

build:
	go build -o $(BIN) $(CMD)

run:
	go run $(CMD) $(ARGS)

tidy:
	go mod tidy

clean:
	rm -f $(BIN)

# Quick smoke tests (uses --dry-run so nothing is mutated)
test-list:
	go run $(CMD) list

test-status:
	go run $(CMD) status

test-apply-dry:
	go run $(CMD) apply --dry-run
