BIN := dotular
CMD := ./cmd/dotular

.PHONY: build run tidy clean index

build:
	go build -o ./build/$(BIN) $(CMD)

run:
	go run $(CMD) $(ARGS)

tidy:
	go mod tidy

clean:
	rm -f ./build/$(BIN)

# Quick smoke tests (uses --dry-run so nothing is mutated)
test-list:
	go run $(CMD) list

test-status:
	go run $(CMD) status

test-apply-dry:
	go run $(CMD) apply --dry-run

index:
	@echo "modules:" > modules/index.yaml
	@for f in modules/*.yaml; do \
		[ "$$(basename "$$f")" = "index.yaml" ] && continue; \
		name=$$(grep '^name:' "$$f" | head -1 | sed 's/^name: *//'); \
		version=$$(grep '^version:' "$$f" | head -1 | sed 's/^version: *//'); \
		echo "  - name: $$name" >> modules/index.yaml; \
		echo "    version: $$version" >> modules/index.yaml; \
	done
	@echo "Generated modules/index.yaml"
